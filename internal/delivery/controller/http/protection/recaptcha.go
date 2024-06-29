package protection

import (
	"bytes"
	recaptcha "cloud.google.com/go/recaptchaenterprise/v2/apiv1"
	recaptchapb "cloud.google.com/go/recaptchaenterprise/v2/apiv1/recaptchaenterprisepb"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
	"io"
	"net/http"
	"time"
)

const (
	siteVerifyURL = "https://www.google.com/recaptcha/api/siteverify"
)

var (
	ErrInvalidRecaptchaToken  = tools.NewError("recaptcha token is invalid", http.StatusBadRequest)
	ErrNoRecaptchaToken       = tools.NewError("recaptcha token is required", http.StatusBadRequest)
	ErrInvalidRecaptchaAction = tools.NewError("recaptcha action is invalid", http.StatusBadRequest)
	ErrLowerScore             = tools.NewError("recaptcha score is lower than required", http.StatusBadRequest)
)

func RequireRecaptcha(action string) gin.HandlerFunc {
	verifyToken := verifyRecaptchaToken
	if protector.config.Recaptcha.ProjectID != "" {
		verifyToken = verifyRecaptchaEnterpriseToken
	}

	return func(ctx *gin.Context) {
		recaptchaToken, err := getRecaptchaToken(ctx)
		if err != nil {
			response.AbortWithError(ctx, ErrNoRecaptchaToken)
			return
		}

		// Check if the recaptcha token is valid
		if err = verifyToken(ctx, recaptchaToken, action); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
	}
}

type siteVerifyRequest struct {
	RecaptchaToken string `json:"recaptchaToken" binding:"required"` //TODO: change to g-recaptcha-response
}

// getRecaptchaToken from request body 'g-recaptcha-response' field
func getRecaptchaToken(ctx *gin.Context) (string, error) {
	bodyBytes, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		return "", err
	}

	var body siteVerifyRequest
	if err = json.Unmarshal(bodyBytes, &body); err != nil {
		return "", err
	}

	// Restore request body to read more than once.
	ctx.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	return body.RecaptchaToken, nil
}

// verifyRecaptchaToken checks if the recaptcha token is valid
func verifyRecaptchaEnterpriseToken(ctx context.Context, token, action string) error {
	ctx = context.Background()
	client, err := recaptcha.NewClient(ctx, option.WithAPIKey(protector.config.Recaptcha.APIKey))
	if err != nil {
		return err
	}
	defer func() {
		if err = client.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close recaptcha client")
		}
	}()

	recaptchaResp, err := client.CreateAssessment(ctx,
		&recaptchapb.CreateAssessmentRequest{
			Assessment: &recaptchapb.Assessment{
				Event: &recaptchapb.Event{
					Token:   token,
					SiteKey: protector.config.Recaptcha.SiteKey,
				},
			},
			Parent: fmt.Sprintf("projects/%s", protector.config.Recaptcha.ProjectID),
		})

	if err != nil {
		return err
	}

	if !recaptchaResp.TokenProperties.Valid {
		return errors.New(recaptchaResp.TokenProperties.InvalidReason.String())
	}

	if recaptchaResp.RiskAnalysis.Score < protector.config.Recaptcha.Score {
		return ErrLowerScore
	}

	if recaptchaResp.TokenProperties.Action != action {
		return ErrInvalidRecaptchaAction
	}

	return nil
}

type siteVerifyResponse struct {
	Success     bool      `json:"success"`
	Score       float32   `json:"score"`
	Action      string    `json:"action"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"errorWrapper-codes"`
}

func verifyRecaptchaToken(ctx context.Context, token, action string) error {
	req, err := http.NewRequest(http.MethodPost, siteVerifyURL, nil)
	if err != nil {
		return err
	}

	// Add necessary request parameters.
	q := req.URL.Query()
	q.Add("secret", protector.config.Recaptcha.SecretKey)
	q.Add("response", token)
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close response body")
		}
	}()

	var body siteVerifyResponse
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}

	log.Debug().Interface("body", body).Msg("Recaptcha response")

	if !body.Success {
		return ErrInvalidRecaptchaToken
	}

	// Check additional response parameters applicable for V3.
	if body.Score < protector.config.Recaptcha.Score {
		return ErrLowerScore
	}

	if body.Action != action {
		return ErrInvalidRecaptchaAction
	}

	return nil
}
