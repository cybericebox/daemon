package protection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

const siteVerifyURL = "https://www.google.com/recaptcha/api/siteverify"

// RequireRecaptcha verifies the request's reCAPTCHA token for the given action.
// Verification is always enforced; the mode is selected by ProjectID:
// set → Enterprise, unset → classic v3.
func (p *Protection) RequireRecaptcha(action string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		token, err := p.getRecaptchaToken(ctx)
		if err != nil {
			response.AbortWithError(ctx, authModel.ErrAuthNoRecaptchaToken.Err())
			return
		}

		verify := p.verifyRecaptchaToken
		if p.recaptcha.ProjectID != "" {
			verify = p.verifyRecaptchaEnterpriseToken
		}
		if err = verify(ctx, token, action); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		ctx.Next()
	}
}

type recaptchaTokenRequest struct {
	RecaptchaToken string `json:"RecaptchaToken"`
}

// getRecaptchaToken reads RecaptchaToken from the JSON body and restores the body
// so the downstream handler can re-bind it.
func (p *Protection) getRecaptchaToken(ctx *gin.Context) (string, error) {
	bodyBytes, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to read request body").Err()
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
	Success bool    `json:"success"`
	Score   float32 `json:"score"`
	Action  string  `json:"action"`
}

// verifyRecaptchaToken validates a classic reCAPTCHA v3 token via the siteverify API.
func (p *Protection) verifyRecaptchaToken(ctx context.Context, token, action string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteVerifyURL, nil)
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to create recaptcha request").
			Err()
	}
	q := req.URL.Query()
	q.Add("secret", p.recaptcha.SecretKey)
	q.Add("response", token)
	req.URL.RawQuery = q.Encode()

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
	return nil
}

// verifyRecaptchaEnterpriseToken validates a token via reCAPTCHA Enterprise.
func (p *Protection) verifyRecaptchaEnterpriseToken(
	ctx context.Context,
	token, action string,
) error {
	client, err := recaptcha.NewClient(ctx, option.WithAPIKey(p.recaptcha.APIKey))
	if err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to create recaptcha client").
			Err()
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			log.Error().Err(cerr).Msg("Failed to close recaptcha client")
		}
	}()

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
	return nil
}
