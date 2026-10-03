package config

import (
	"testing"
	"time"
)

func TestDOSConfigValidate(t *testing.T) {
	ok := DOSConfig{Protection: "on", ClientTokenTTL: time.Hour, NoTokenPerMinute: 1, NoTokenBurst: 1,
		ClientPerMinute: 1, ClientBurst: 1, AuthPerMinute: 1, AuthBurst: 1, EventPerMinute: 1, EventBurst: 1}
	if err := ok.Validate(); err != nil || !ok.Enabled() {
		t.Fatalf("valid config: %v", err)
	}
	for name, mutate := range map[string]func(*DOSConfig){
		"unknown mode": func(c *DOSConfig) { c.Protection = "yes" },
		"short ttl":    func(c *DOSConfig) { c.ClientTokenTTL = time.Second },
		"zero bucket":  func(c *DOSConfig) { c.AuthBurst = 0 },
	} {
		c := ok
		mutate(&c)
		if c.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	off := ok
	off.Protection = "off"
	if off.Enabled() {
		t.Fatal("off is not enabled")
	}
}

func TestDOSDefaultsAndCaptchaDefaultsFromTheEnvironment(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	cfg := MustGetConfig()
	if cfg.DOS.Protection != "off" || cfg.DOS.ClientTokenTTL != 24*time.Hour || cfg.Auth.Captcha.Provider != CaptchaRecaptcha {
		t.Fatalf("defaults: %+v / %+v", cfg.DOS, cfg.Auth.Captcha)
	}
}

func TestValidateCaptcha(t *testing.T) {
	tests := map[string]struct {
		auth    AuthConfig
		wantErr bool
	}{
		"turnstile with secret":            {AuthConfig{Captcha: CaptchaConfig{CaptchaTurnstile}, Turnstile: TurnstileConfig{Secret: "s"}}, false},
		"turnstile without secret":         {AuthConfig{Captcha: CaptchaConfig{CaptchaTurnstile}}, true},
		"turnstile ignores recaptcha keys": {AuthConfig{Captcha: CaptchaConfig{CaptchaTurnstile}, Recaptcha: RecaptchaConfig{SecretKey: "r"}}, true},
		"recaptcha classic":                {AuthConfig{Captcha: CaptchaConfig{CaptchaRecaptcha}, Recaptcha: RecaptchaConfig{SecretKey: "r"}}, false},
		"recaptcha without keys":           {AuthConfig{Captcha: CaptchaConfig{CaptchaRecaptcha}}, true},
		"none needs nothing":               {AuthConfig{Captcha: CaptchaConfig{CaptchaNone}}, false},
		"unknown provider":                 {AuthConfig{Captcha: CaptchaConfig{"hcaptcha"}}, true},
	}
	for name, tc := range tests {
		if err := tc.auth.ValidateCaptcha(); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCaptchaNoneIsAWeaknessAndTheOtherProvidersAreNot(t *testing.T) {
	has := func(c AuthConfig) bool {
		for _, w := range c.Weaknesses() {
			if len(w) > 16 && w[:16] == "CAPTCHA_PROVIDER" {
				return true
			}
		}
		return false
	}
	if !has(AuthConfig{Captcha: CaptchaConfig{CaptchaNone}}) || has(AuthConfig{Captcha: CaptchaConfig{CaptchaTurnstile}}) {
		t.Fatal("only none is a weakness")
	}
}

func TestCaptchaProviderAndDOSFromTheEnvironment(t *testing.T) {
	t.Setenv("CAPTCHA_PROVIDER", "turnstile")
	t.Setenv("TURNSTILE_SECRET", "ts")
	t.Setenv("DOS_PROTECTION", "on")
	t.Setenv("DOS_CLIENT_TOKEN_TTL", "2h")
	cfg := MustGetConfig()
	if cfg.Auth.Captcha.Provider != CaptchaTurnstile || cfg.Auth.Turnstile.Secret != "ts" || !cfg.DOS.Enabled() || cfg.DOS.ClientTokenTTL != 2*time.Hour {
		t.Fatalf("%+v %+v", cfg.Auth.Captcha, cfg.DOS)
	}
}
