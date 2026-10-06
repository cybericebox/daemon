package protection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	recaptcha "cloud.google.com/go/recaptchaenterprise/v2/apiv1"
	"cloud.google.com/go/recaptchaenterprise/v2/apiv1/recaptchaenterprisepb"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

var recaptchaHTTPClient = &http.Client{Timeout: 10 * time.Second}

// maxRecaptchaBodyBytes bounds the sign-in and sign-up bodies read before authentication.
const maxRecaptchaBodyBytes = 64 << 10

// siteVerifyURL is a variable so a test can point it at a local server.
var siteVerifyURL = "https://www.google.com/recaptcha/api/siteverify"

// RequireCaptcha verifies the request's bot-check token for the given action with the platform's provider
// (CAPTCHA_PROVIDER). Verification is always enforced. The token travels in the JSON body as RecaptchaToken
// whichever provider issued it (the field keeps its first name).
func (p *Protection) RequireCaptcha(action string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		token, err := p.getRecaptchaToken(ctx)
		if err != nil {
			response.AbortWithError(ctx, authModel.ErrAuthNoRecaptchaToken.Err())
			return
		}
		if err = p.captcha.Verify(ctx, token, action); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		ctx.Next()
	}
}

// recaptchaVerifier is the reCAPTCHA provider: the mode is selected by ProjectID, set → Enterprise,
// unset → classic v3.
type recaptchaVerifier struct{ p *Protection }

func (v recaptchaVerifier) Verify(ctx context.Context, token, action string) error {
	if v.p.recaptcha.ProjectID != "" {
		return v.p.verifyRecaptchaEnterpriseToken(ctx, token, action)
	}
	return v.p.verifyRecaptchaToken(ctx, token, action)
}

type recaptchaTokenRequest struct {
	RecaptchaToken string `json:"RecaptchaToken"`
}

// getRecaptchaToken reads RecaptchaToken from the JSON body and restores the body
// so the downstream handler can re-bind it.
func (p *Protection) getRecaptchaToken(ctx *gin.Context) (string, error) {
	// The token request is a small JSON body; this runs before authentication, so it is read with a cap.
	bodyBytes, err := io.ReadAll(io.LimitReader(ctx.Request.Body, maxRecaptchaBodyBytes+1))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to read request body").Err()
	}
	if len(bodyBytes) > maxRecaptchaBodyBytes {
		return "", authModel.ErrAuthNoRecaptchaToken.Err()
	}
	ctx.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var body recaptchaTokenRequest
	if err = json.Unmarshal(bodyBytes, &body); err != nil {
		return "", model.ErrPlatform.WithError(err).
			WithMessage("Failed to unmarshal request body").
			Err()
	}
	if body.RecaptchaToken == "" {
		return "", authModel.ErrAuthNoRecaptchaToken.Err()
	}
	return body.RecaptchaToken, nil
}

type siteVerifyResponse struct {
	Success  bool    `json:"success"`
	Score    float32 `json:"score"`
	Action   string  `json:"action"`
	Hostname string  `json:"hostname"`
}

// requireRecaptchaHost accepts a token only when it was solved on one of our
// own frontends: a token from the same site key on someone else's page (or a
// solver service's page) proves nothing about a visitor of ours.
func (p *Protection) requireRecaptchaHost(hostname string) error {
	if hostname == "" || !p.hosts.IsFrontendOrigin(strings.ToLower(strings.TrimSuffix(hostname, "."))) {
		return authModel.ErrAuthInvalidRecaptchaToken.WithError(fmt.Errorf("recaptcha token hostname %q is not a platform frontend", hostname)).Err()
	}
	return nil
}

// verifyRecaptchaToken validates a classic reCAPTCHA v3 token via the siteverify API.
func (p *Protection) verifyRecaptchaToken(ctx context.Context, token, action string) error {
	// The secret travels in the POST body, never in the URL: a failed request is logged with its URL.
	form := url.Values{"secret": {p.recaptcha.SecretKey}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return model.ErrPlatform.WithError(errors.New("cannot build the verification request")).
			WithMessage("Failed to create recaptcha request").
			Err()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := recaptchaHTTPClient.Do(req)
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to send recaptcha request").
			Err()
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Error().Err(cerr).Msg("Failed to close recaptcha response body")
		}
	}()

	var body siteVerifyResponse
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to decode recaptcha response").
			Err()
	}
	if !body.Success {
		return authModel.ErrAuthInvalidRecaptchaToken.Err()
	}
	if body.Score < p.recaptcha.Score {
		return authModel.ErrAuthLowerScore.WithError(errors.New(fmt.Sprintf("%f", body.Score))).
			Err()
	}
	if body.Action != action {
		return authModel.ErrAuthInvalidRecaptchaAction.WithError(errors.New(body.Action)).Err()
	}
	return p.requireRecaptchaHost(body.Hostname)
}

// enterpriseClient returns the shared reCAPTCHA Enterprise client, dialled on
// first use (a client per request opened a new gRPC connection on every
// sign-in). A failed dial is not cached.
func (p *Protection) enterpriseClient() (*recaptcha.Client, error) {
	p.recaptchaMu.Lock()
	defer p.recaptchaMu.Unlock()
	if p.recaptchaClient != nil {
		return p.recaptchaClient, nil
	}
	// The client outlives the request that created it: its connection must not
	// be tied to that request's context.
	client, err := recaptcha.NewClient(context.Background(), option.WithAPIKey(p.recaptcha.APIKey))
	if err != nil {
		return nil, err
	}
	p.recaptchaClient = client
	return client, nil
}

// verifyRecaptchaEnterpriseToken validates a token via reCAPTCHA Enterprise.
func (p *Protection) verifyRecaptchaEnterpriseToken(
	ctx context.Context,
	token, action string,
) error {
	client, err := p.enterpriseClient()
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to create recaptcha client").
			Err()
	}

	resp, err := client.CreateAssessment(
		ctx, &recaptchaenterprisepb.CreateAssessmentRequest{
			Assessment: &recaptchaenterprisepb.Assessment{
				Event: &recaptchaenterprisepb.Event{Token: token, SiteKey: p.recaptcha.SiteKey},
			},
			Parent: fmt.Sprintf("projects/%s", p.recaptcha.ProjectID),
		},
	)
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to create recaptcha assessment").
			Err()
	}
	if !resp.TokenProperties.Valid {
		return authModel.ErrAuthInvalidRecaptchaToken.WithError(errors.New(resp.TokenProperties.InvalidReason.String())).
			Err()
	}
	if resp.RiskAnalysis.Score < p.recaptcha.Score {
		return authModel.ErrAuthLowerScore.WithError(errors.New(fmt.Sprintf("%f", resp.RiskAnalysis.Score))).
			Err()
	}
	if resp.TokenProperties.Action != action {
		return authModel.ErrAuthInvalidRecaptchaAction.WithError(errors.New(resp.TokenProperties.Action)).
			Err()
	}
	return p.requireRecaptchaHost(resp.TokenProperties.Hostname)
}
