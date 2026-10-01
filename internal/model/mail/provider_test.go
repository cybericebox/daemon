package mailModel

import (
	"errors"
	"fmt"
	"net/textproto"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func provider(name string, priority int, enabled bool) SMTPConfig {
	return SMTPConfig{
		ID: uuid.NewV5(uuid.Nil, name), Name: name, Priority: priority, Enabled: enabled,
		CreatedAt: now.Add(-time.Hour),
	}
}

func names(l []SMTPConfig) []string {
	out := make([]string, len(l))
	for i, p := range l {
		out[i] = p.Name
	}
	return out
}

func TestSelectProvidersOrdersByPriorityAndSkipsDisabled(t *testing.T) {
	got := SelectProviders([]SMTPConfig{provider("c", 2, true), provider("off", 0, false), provider("b", 1, true), provider("a", 0, true)}, now)
	if fmt.Sprint(names(got)) != "[a b c]" {
		t.Fatalf("order = %v", names(got))
	}
}

func TestSelectProvidersTiesBreakByCreation(t *testing.T) {
	older, newer := provider("older", 1, true), provider("newer", 1, true)
	newer.CreatedAt = now
	got := SelectProviders([]SMTPConfig{newer, older}, now)
	if fmt.Sprint(names(got)) != "[older newer]" {
		t.Fatalf("order = %v", names(got))
	}
}

func TestSelectProvidersSkipsProviderOverDailyLimit(t *testing.T) {
	full := provider("full", 0, true)
	full.DailyQuota = new(5)
	full.Usage = Usage{Day: Today(now), SentToday: 5}
	open := provider("open", 1, true)
	got := SelectProviders([]SMTPConfig{full, open}, now)
	if fmt.Sprint(names(got)) != "[open]" {
		t.Fatalf("order = %v", names(got))
	}
}

func TestSelectProvidersCounterOfPreviousDayIsZero(t *testing.T) {
	p := provider("p", 0, true)
	p.DailyQuota = new(5)
	p.Usage = Usage{Day: Today(now).Add(-24 * time.Hour), SentToday: 5}
	if got := SelectProviders([]SMTPConfig{p}, now); len(got) != 1 {
		t.Fatalf("a count of yesterday must not block today: %v", names(got))
	}
	if p.Usage.SentOn(now) != 0 {
		t.Fatalf("SentOn = %d", p.Usage.SentOn(now))
	}
}

func TestSelectProvidersNoLimitIsUnlimited(t *testing.T) {
	p := provider("p", 0, true)
	p.Usage = Usage{Day: Today(now), SentToday: 1_000_000}
	if got := SelectProviders([]SMTPConfig{p}, now); len(got) != 1 {
		t.Fatal("a provider without a daily limit is never exhausted")
	}
}

func TestSelectProvidersFailingProviderGoesLastDuringCooldown(t *testing.T) {
	failed := provider("failed", 0, true)
	errAt := now.Add(-10 * time.Second)
	failed.Usage = Usage{LastError: "535 auth", LastErrorAt: &errAt}
	ok := provider("ok", 1, true)
	got := SelectProviders([]SMTPConfig{failed, ok}, now)
	if fmt.Sprint(names(got)) != "[ok failed]" {
		t.Fatalf("order = %v", names(got))
	}

	// After the cooldown it is first again.
	old := now.Add(-2 * ErrorCooldown)
	failed.Usage.LastErrorAt = &old
	got = SelectProviders([]SMTPConfig{failed, ok}, now)
	if fmt.Sprint(names(got)) != "[failed ok]" {
		t.Fatalf("after cooldown order = %v", names(got))
	}
}

func TestSelectProvidersSuccessAfterErrorEndsCooldown(t *testing.T) {
	p := provider("p", 0, true)
	errAt, usedAt := now.Add(-20*time.Second), now.Add(-10*time.Second)
	p.Usage = Usage{LastError: "x", LastErrorAt: &errAt, LastUsedAt: &usedAt}
	other := provider("other", 1, true)
	got := SelectProviders([]SMTPConfig{p, other}, now)
	if fmt.Sprint(names(got)) != "[p other]" {
		t.Fatalf("order = %v", names(got))
	}
}

func TestAllExhausted(t *testing.T) {
	full := provider("full", 0, true)
	full.DailyQuota = new(1)
	full.Usage = Usage{Day: Today(now), SentToday: 1}
	if !AllExhausted([]SMTPConfig{full}, now) {
		t.Fatal("every enabled provider is over its limit")
	}
	if AllExhausted([]SMTPConfig{full, provider("open", 1, true)}, now) {
		t.Fatal("one provider still has room")
	}
	if AllExhausted([]SMTPConfig{provider("off", 0, false)}, now) {
		t.Fatal("no enabled provider is not exhaustion")
	}
	if AllExhausted(nil, now) {
		t.Fatal("empty list is not exhaustion")
	}
}

func TestNextResetIsNextUTCMidnight(t *testing.T) {
	got := NextReset(time.Date(2026, 10, 1, 23, 59, 0, 0, time.FixedZone("x", 3*3600)))
	if want := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("NextReset = %v, want %v", got, want)
	}
}

func TestProviderFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"auth", fmt.Errorf("send: %w", &textproto.Error{Code: 535}), true},
		{"quota", &textproto.Error{Code: 552}, true},
		{"unavailable", &textproto.Error{Code: 421}, true},
		{"mailbox rejected", &textproto.Error{Code: 550}, false},
		{"recipient invalid", &textproto.Error{Code: 553}, false},
		{"greylist", &textproto.Error{Code: 451}, false},
		{"dial", errors.New("dial tcp 1.2.3.4:587: connect: connection refused"), true},
		{"unrelated", errors.New("template broken"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := ProviderFailure(c.err); got != c.want {
			t.Errorf("%s: ProviderFailure = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProviderInputNormalize(t *testing.T) {
	good := ProviderInput{SMTPInput: SMTPInput{Host: "smtp.example.com", Port: 587}, Name: "  Primary "}
	out, err := good.Normalize()
	if err != nil || out.Name != "Primary" {
		t.Fatalf("Normalize = %+v, %v", out, err)
	}
	for _, bad := range []ProviderInput{
		{SMTPInput: good.SMTPInput, Name: ""},
		{SMTPInput: good.SMTPInput, Name: string(make([]byte, 0)) + "x\ny"},
		{SMTPInput: SMTPInput{Host: "", Port: 587}, Name: "n"},
		{SMTPInput: good.SMTPInput, Name: "n", Identity: Identity{FromAddress: "not an address"}},
	} {
		if _, err := bad.Normalize(); err == nil {
			t.Errorf("expected an error for %+v", bad)
		}
	}
}
