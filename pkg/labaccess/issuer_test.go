package labaccess

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testSigningKey(t *testing.T) SigningKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return SigningKey{Tenant: "platform", KeyID: "k-1", Key: key}
}

func TestNewRefusesANegativeTTL(t *testing.T) {
	if _, err := New(Config{TokenTTL: -time.Second}); err == nil {
		t.Fatal("a negative ttl must be refused")
	}
}

const webURL = "https://web-abc123.challenges.example.com/login"

func TestIssueSignsAHandoffLinkTheProxyCanVerify(t *testing.T) {
	sk := testSigningKey(t)
	issuer, err := New(Config{TokenTTL: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	session := Session{Group: "e-0190-t-0191", Client: "p-0192", AccessURL: webURL}

	link, err := issuer.Issue(sk, session, now)
	if err != nil {
		t.Fatal(err)
	}
	parsedURL, err := url.Parse(link.URL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "web-abc123.challenges.example.com" || parsedURL.Path != AuthPath || parsedURL.Query().Get("t") != link.Token {
		t.Fatalf("link = %s err = %v", link.URL, err)
	}
	var got claims
	parsed, err := jwt.ParseWithClaims(link.Token, &got, func(*jwt.Token) (any, error) { return sk.Key.Public(), nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer("platform"), jwt.WithAudience(Audience),
		jwt.WithTimeFunc(func() time.Time { return now.Add(10 * time.Second) }))
	if err != nil || !parsed.Valid {
		t.Fatalf("the proxy could not verify the token: %v", err)
	}
	if parsed.Header["kid"] != "k-1" {
		t.Fatalf("kid header = %v, want the key id the proxy picks the key by", parsed.Header["kid"])
	}
	if got.GroupID != session.Group || got.Host != "web-abc123" || got.Subject != session.Client || got.Issuer != "platform" || got.NotBefore == nil || !got.NotBefore.Time.Equal(now) {
		t.Fatalf("claims = %+v", got)
	}
	if got.Session != now.Add(DefaultSessionTTL).Unix() || !link.ExpiresAt.Equal(now.Add(DefaultSessionTTL)) {
		t.Fatalf("session end = %d / %s", got.Session, link.ExpiresAt)
	}
	// The link itself is short-lived.
	if !got.ExpiresAt.Time.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("link exp = %s", got.ExpiresAt.Time)
	}
	// Only operator identifiers: no platform user, event or team id.
	raw, _ := json.Marshal(got)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	for _, forbidden := range []string{"user_id", "evt", "team", "test", "client", "ver"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("claim %q must not be in the token: %s", forbidden, raw)
		}
	}
	// The token is stateless: no jti, and it is refused after its TTL.
	if got.ID != "" {
		t.Fatalf("no jti expected, got %q", got.ID)
	}
	if _, err = jwt.ParseWithClaims(link.Token, &claims{}, func(*jwt.Token) (any, error) { return sk.Key.Public(), nil },
		jwt.WithTimeFunc(func() time.Time { return now.Add(time.Minute) })); err == nil {
		t.Fatal("an expired link must be refused")
	}
	// Another tenant's key does not verify it.
	other := testSigningKey(t)
	if _, err = jwt.ParseWithClaims(link.Token, &claims{}, func(*jwt.Token) (any, error) { return other.Key.Public(), nil },
		jwt.WithTimeFunc(func() time.Time { return now })); err == nil {
		t.Fatal("a token must not verify with another tenant's key")
	}
}

func TestIssueRefusesAnIncompleteSessionOrKey(t *testing.T) {
	sk := testSigningKey(t)
	issuer, _ := New(Config{})
	if _, err := issuer.Issue(sk, Session{Group: "g", AccessURL: webURL}, time.Now()); err == nil {
		t.Fatal("a session without a client must be refused")
	}
	if _, err := issuer.Issue(sk, Session{Group: "g", Client: "p-1"}, time.Now()); err == nil {
		t.Fatal("a device without a web address must be refused")
	}
	for name, bad := range map[string]SigningKey{
		"no tenant": {KeyID: "k", Key: sk.Key}, "no key id": {Tenant: "t", Key: sk.Key}, "no key": {Tenant: "t", KeyID: "k"},
	} {
		if _, err := issuer.Issue(bad, Session{Group: "g", Client: "p-1", AccessURL: webURL}, time.Now()); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

func TestIssueLivesUntilTheRequestedEnd(t *testing.T) {
	sk := testSigningKey(t)
	issuer, _ := New(Config{TokenTTL: 30 * time.Second})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	session := Session{Group: "g", Client: "p-1", AccessURL: webURL}
	sessionEnd := func(l Link) int64 {
		var got claims
		_, _ = jwt.ParseWithClaims(l.Token, &got, func(*jwt.Token) (any, error) { return sk.Key.Public(), nil }, jwt.WithTimeFunc(func() time.Time { return now }))
		return got.Session
	}

	session.ExpiresAt = now.Add(72 * time.Hour)
	link, err := issuer.Issue(sk, session, now)
	if err != nil {
		t.Fatal(err)
	}
	if !link.ExpiresAt.Equal(session.ExpiresAt) || sessionEnd(link) != session.ExpiresAt.Unix() {
		t.Fatalf("session must last until the event end: %s", link.ExpiresAt)
	}

	// An end that is not in the future falls back to the TTL.
	session.ExpiresAt = now.Add(-time.Hour)
	link, _ = issuer.Issue(sk, session, now)
	if !link.ExpiresAt.Equal(now.Add(DefaultSessionTTL)) || sessionEnd(link) != now.Add(DefaultSessionTTL).Unix() {
		t.Fatalf("fallback ttl expected, got %s", link.ExpiresAt)
	}
}

func TestDefaultTokenTTLAndSessionFallback(t *testing.T) {
	sk := testSigningKey(t)
	issuer, _ := New(Config{})
	now := time.Unix(5000, 0)
	link, _ := issuer.Issue(sk, Session{Group: "g", Client: "p-1", AccessURL: webURL}, now)
	if !link.ExpiresAt.Equal(now.Add(DefaultSessionTTL)) {
		t.Fatalf("fallback session end = %s", link.ExpiresAt)
	}
	var got claims
	_, _ = jwt.ParseWithClaims(link.Token, &got, func(*jwt.Token) (any, error) { return nil, nil }, jwt.WithoutClaimsValidation())
	if got.ExpiresAt.Unix() != now.Unix()+int64(DefaultTokenTTL.Seconds()) {
		t.Fatalf("default ttl = %v", got.ExpiresAt)
	}
}

func TestTheAgentsProxyLimitsCapTheLinkAndTheSession(t *testing.T) {
	sk := testSigningKey(t)
	sk.MaxTokenTTL, sk.MaxSessionTTL = 2*time.Minute, time.Hour
	issuer, err := New(Config{TokenTTL: 10 * time.Minute, SessionTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	link, err := issuer.Issue(sk, Session{Group: "e-1-t-1", Client: "p-1", AccessURL: webURL}, now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(link.Token, &claims{})
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Claims.(*claims).Session; got != now.Add(time.Hour).Unix() || !link.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("the session ends at %d, want the proxy's hour", got)
	}
	exp, _ := parsed.Claims.GetExpirationTime()
	if !exp.Time.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("the link lives until %v, want the proxy's two minutes", exp.Time)
	}
	// An event that ends sooner than the proxy's limit keeps its own end; without a report nothing is capped.
	short, _ := issuer.Issue(sk, Session{Group: "e-1-t-1", Client: "p-1", AccessURL: webURL, ExpiresAt: now.Add(10 * time.Minute)}, now)
	if !short.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("session = %v", short.ExpiresAt)
	}
	sk.MaxTokenTTL, sk.MaxSessionTTL = 0, 0
	free, _ := issuer.Issue(sk, Session{Group: "e-1-t-1", Client: "p-1", AccessURL: webURL}, now)
	if !free.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("no report: session = %v", free.ExpiresAt)
	}
	if _, err := New(Config{SessionTTL: -time.Hour}); err == nil {
		t.Fatal("a negative session ttl must be rejected")
	}
}
