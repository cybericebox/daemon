package protection

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"time"
)

const (
	siteVerifyURL = "https://www.google.com/recaptcha/api/siteverify"

	// Action constants
	SignIn         = "signIn"
	SignUp         = "signUp"
	ForgotPassword = "forgotPassword"
)

var (
	ErrInvalidRecaptchaToken  = errors.New("recaptcha token is invalid")
	ErrNoRecaptchaToken       = errors.New("recaptcha token is required")
	ErrInvalidRecaptchaAction = errors.New("recaptcha action is invalid")
	ErrLowerScore             = errors.New("recaptcha score is lower than required")
)

// ProtectAPIRouteWithRecaptcha protects API route with recaptcha
func ProtectAPIRouteWithRecaptcha(action string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		captchaToken, err := getRecaptchaToken(ctx)
		if err != nil {
			response.AbortWithBadRequest(ctx, ErrNoRecaptchaToken)
			return
		}

		// Check if the recaptcha token is valid
		log.Debug().Str("recaptchaToken", captchaToken).Msg("Validating recaptcha token")
		if err = verifyRecaptchaToken(captchaToken, action); err != nil {
			response.AbortWithUnauthorized(ctx, err)
			return
		}

		ctx.Next()
	}
}

type siteVerifyRequest struct {
	RecaptchaResponse string `json:"g-recaptcha-response"`
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

	return body.RecaptchaResponse, nil
}

type siteVerifyResponse struct {
	Success     bool      `json:"success"`
	Score       float64   `json:"score"`
	Action      string    `json:"action"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes"`
}

// verifyRecaptchaToken checks if the recaptcha token is valid
func verifyRecaptchaToken(response, action string) error {
	req, err := http.NewRequest(http.MethodPost, siteVerifyURL, nil)
	if err != nil {
		return err
	}

	// Add necessary request parameters.
	q := req.URL.Query()
	q.Add("secret", routesProtector.config.RecaptchaV3.SecretKey)
	q.Add("response", response)
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
	if body.Score < routesProtector.config.RecaptchaV3.Score {
		return ErrLowerScore
	}

	if body.Action != action {
		return ErrInvalidRecaptchaAction
	}

	return nil
}
