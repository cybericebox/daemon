package config_test

import (
	"testing"

	"github.com/caarlos0/env/v11"

	"github.com/cybericebox/daemon/internal/config"
)

func TestRateLimitConfigDefaultsAndValidation(t *testing.T) {
	var c config.RateLimitConfig
	if err := env.ParseWithOptions(&c, env.Options{Prefix: "RATE_LIMIT_", Environment: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if c.UserPerMinute != 1200 || c.UserBurst != 600 || c.AnonPerMinute != 12000 || c.AnonBurst != 6000 {
		t.Fatalf("defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.AnonBurst = 0
	if c.Validate() == nil {
		t.Fatal("zero burst must be refused")
	}
	if err := env.ParseWithOptions(&c, env.Options{Prefix: "RATE_LIMIT_", Environment: map[string]string{"RATE_LIMIT_USER_PER_MINUTE": "7"}}); err != nil || c.UserPerMinute != 7 {
		t.Fatalf("env key RATE_LIMIT_USER_PER_MINUTE: %v %+v", err, c)
	}
}
