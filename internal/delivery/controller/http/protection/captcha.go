package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

// CaptchaVerifier checks a token of the platform's bot-check provider. action is the name the frontend
// executed the check with (the provider reports it back where it supports that).
type CaptchaVerifier interface {
	Verify(ctx context.Context, token, action string) error
}

// newCaptchaVerifier builds the verifier of CAPTCHA_PROVIDER.
func (p *Protection) newCaptchaVerifier(cfg config.AuthConfig) CaptchaVerifier {
	switch cfg.Captcha.Provider {
	case config.CaptchaTurnstile:
		return &turnstileVerifier{secret: cfg.Turnstile.Secret, hosts: p.hosts}
	case config.CaptchaNone:
		return noneVerifier{}
	}
	return recaptchaVerifier{p}
}

// noneVerifier accepts every token: local development and tests (CAPTCHA_PROVIDER=none).
type noneVerifier struct{}

func (noneVerifier) Verify(context.Context, string, string) error { return nil }

var turnstileHTTPClient = &http.Client{Timeout: 10 * time.Second}

// turnstileVerifyURL is a variable so a test can point it at a local server.
var turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileVerifier is Cloudflare Turnstile: siteverify with the secret, then the hostname the widget was
// solved on must be a platform frontend.
type turnstileVerifier struct {
	secret string
	hosts  config.HostsConfig
}

type turnstileResponse struct {
	Success  bool   `json:"success"`
	Action   string `json:"action"`
	Hostname string `json:"hostname"`
}

func (v *turnstileVerifier) Verify(ctx context.Context, token, action string) error {
	// The secret travels in the POST body, never in the URL: a failed request is logged with its URL.
	form := url.Values{"secret": {v.secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return model.ErrPlatform.WithError(errors.New("cannot build the verification request")).
			WithMessage("Failed to create turnstile request").Err()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := turnstileHTTPClient.Do(req)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send turnstile request").Err()
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Error().Err(cerr).Msg("Failed to close turnstile response body")
		}
	}()

	var body turnstileResponse
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to decode turnstile response").Err()
	}
	if !body.Success {
		return authModel.ErrAuthInvalidRecaptchaToken.Err()
	}
	// The widget reports the action only when the frontend set one.
	if body.Action != "" && body.Action != action {
		return authModel.ErrAuthInvalidRecaptchaAction.WithError(errors.New(body.Action)).Err()
	}
	hostname := strings.ToLower(strings.TrimSuffix(body.Hostname, "."))
	if hostname == "" || !v.hosts.IsFrontendOrigin(hostname) {
		return authModel.ErrAuthInvalidRecaptchaToken.
			WithError(fmt.Errorf("turnstile token hostname %q is not a platform frontend", body.Hostname)).Err()
	}
	return nil
}
