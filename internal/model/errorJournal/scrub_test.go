package errorJournal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScrubRemovesSecrets(t *testing.T) {
	cases := map[string]struct{ in, leak string }{
		"email":          {"send to anna.k@example.org failed", "anna.k@example.org"},
		"password kv":    {"login failed password=hunter2 for user", "hunter2"},
		"json password":  {`body {"password":"s3cr3t-value","a":1}`, "s3cr3t-value"},
		"prefixed key":   {"db_password: abc123xyz", "abc123xyz"},
		"bearer":         {"header Authorization: Bearer abcdefghijklmnop123", "abcdefghijklmnop123"},
		"jwt":            {"bad token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV", "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV"},
		"url userinfo":   {"dial postgres://admin:topsecret@db.internal:5432/x", "topsecret"},
		"query token":    {"GET /cb?code=ABCDEF123456&x=1", "ABCDEF123456"},
		"telegram token": {"POST https://api.telegram.org/bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw/send", "AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"},
		"ipv4":           {"dial tcp 10.1.2.3:443: refused", "10.1.2.3"},
		"ipv6":           {"dial tcp [2001:db8::1]:443", "2001:db8::1"},
		"long secret":    {"key a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8 used", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8"},
		"pem":            {"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkq\n-----END PRIVATE KEY-----", "MIIEvQIBADANBgkq"},
		"aws":            {"id AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.NotContains(t, Scrub(c.in), c.leak)
		})
	}
}

func TestScrubKeepsHarmlessText(t *testing.T) {
	in := "event 0198c1f2-7b3a-7c11-9d2e-3f4a5b6c7d8e not found at 12:30:45 (status 404)"
	assert.Equal(t, in, Scrub(in))
}

func TestTruncateKeepsRunes(t *testing.T) {
	out := Truncate(strings.Repeat("ї", 100), 11)
	assert.True(t, strings.HasSuffix(out, "…"))
	assert.Equal(t, strings.Repeat("ї", 5)+"…", out)
}

func TestFingerprintGroupsAlikeMessages(t *testing.T) {
	a := Event{Kind: KindHTTP5xx, Source: "/api/x/:id", Message: "timeout after 31s for 0198c1f2-7b3a-7c11-9d2e-3f4a5b6c7d8e (x.go:12)"}
	b := Event{Kind: KindHTTP5xx, Source: "/api/x/:id", Message: "timeout after 45s for 0198c1f2-0000-7c11-9d2e-3f4a5b6c7d8f (x.go:13)"}
	c := Event{Kind: KindHTTP5xx, Source: "/api/y", Message: a.Message}
	assert.Equal(t, Fingerprint(a), Fingerprint(b))
	assert.NotEqual(t, Fingerprint(a), Fingerprint(c))
}

func TestFingerprintOfRefusalsIgnoresMessageAndUser(t *testing.T) {
	a := Event{Kind: KindHTTP403, Route: "/api/a", Role: "admin", Permission: "users.read", Message: "one"}
	b := Event{Kind: KindHTTP403, Route: "/api/a", Role: "admin", Permission: "users.read", Message: "two"}
	c := Event{Kind: KindHTTP403, Route: "/api/a", Role: "user", Permission: "users.read"}
	assert.Equal(t, Fingerprint(a), Fingerprint(b))
	assert.NotEqual(t, Fingerprint(a), Fingerprint(c))
}

func TestFingerprintOfRateLimitUsesLimiter(t *testing.T) {
	a := Event{Kind: KindHTTP429, Route: "/api/a", Limiter: "per-user"}
	b := Event{Kind: KindHTTP429, Route: "/api/a", Limiter: "auth"}
	assert.NotEqual(t, Fingerprint(a), Fingerprint(b))
}

func TestDefaultRulesFollowTheSpec(t *testing.T) {
	assert.Equal(t, NotifyAlways, DefaultRule(KindPanic))
	assert.Equal(t, NotifyNew, DefaultRule(KindHTTP403))
	assert.Equal(t, NotifySpike, DefaultRule(KindHTTP429))
	assert.Equal(t, NotifyNewOrSpike, DefaultRule(KindHTTP5xx))
	assert.Equal(t, NotifyAlways, DefaultRule(KindJob))
	assert.Equal(t, NotifyNew, DefaultRule(KindMail))
	assert.Equal(t, NotifyAlways, DefaultRule(KindLabAgentOffline))
}
