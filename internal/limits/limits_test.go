package limits_test

import (
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/limits"
)

func TestDefaultsAreTheDocumentedOnes(t *testing.T) {
	c := limits.Get()
	if c.SignInMaxFailures != 5 || c.SignInFailureWindow != 15*time.Minute || c.SignInLockBase != time.Minute || c.SignInLockMax != 15*time.Minute {
		t.Fatalf("sign-in defaults: %+v", c)
	}
	if c.AccountMailGap != time.Minute || c.AccountMailPerHour != 3 || c.EmailChangesPerHour != 5 {
		t.Fatalf("mail defaults: %+v", c)
	}
	if c.AccountActions != 20 || c.PreviewPerMinute != 60 || c.LiveScreenPerMinute != 120 {
		t.Fatalf("request defaults: %+v", c)
	}
	if c.StreamsPerUser != 8 || c.StreamsPerScreen != 10 || c.StreamsAnonymousPerEvent != 500 || c.AttemptStreamsPerUser != 6 || c.ErrorStreamsPerUser != 3 {
		t.Fatalf("stream defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestValidateRefusesZero(t *testing.T) {
	c := limits.Get()
	c.AccountMailPerHour = 0
	if c.Validate() == nil {
		t.Fatal("a zero count must be refused")
	}
	c = limits.Get()
	c.SignInLockBase = 0
	if c.Validate() == nil {
		t.Fatal("a zero duration must be refused")
	}
}

func TestSetOverridesTheDefaults(t *testing.T) {
	t.Cleanup(func() { limits.Set(limits.Get()) })
	c := limits.Get()
	c.PreviewPerMinute = 7
	limits.Set(c)
	if limits.Get().PreviewPerMinute != 7 {
		t.Fatal("Set must win")
	}
}
