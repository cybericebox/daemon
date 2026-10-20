package authModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

var (
	sessionNow = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	idleTTL    = 720 * time.Hour
)

func TestNewSession_SetsDefaultsInDomain(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	meta := authModel.SessionMetadata{UserAgent: "ua", IP: "1.2.3.4"}

	s := authModel.NewSession(userID, meta, idleTTL, sessionNow)

	if s.ID == uuid.Nil {
		t.Fatal("NewSession must generate the ID in the domain (not the DB)")
	}
	if s.UserID != userID {
		t.Fatalf("UserID = %v, want %v", s.UserID, userID)
	}
	if !s.ExpiresAt.Equal(sessionNow.Add(idleTTL)) {
		t.Fatalf("ExpiresAt = %v, want now+idleTTL %v", s.ExpiresAt, sessionNow.Add(idleTTL))
	}
	if !s.LastSeen.Equal(sessionNow) || !s.CreatedAt.Equal(sessionNow) {
		t.Fatalf("LastSeen/CreatedAt must default to now: %v / %v", s.LastSeen, s.CreatedAt)
	}
	if s.Metadata != meta {
		t.Fatalf("Metadata = %+v, want %+v", s.Metadata, meta)
	}
}

func TestSession_IsExpired(t *testing.T) {
	s := authModel.Session{ExpiresAt: sessionNow}
	if s.IsExpired(sessionNow.Add(-time.Second)) {
		t.Fatal("not expired before ExpiresAt")
	}
	if !s.IsExpired(sessionNow.Add(time.Second)) {
		t.Fatal("expired after ExpiresAt")
	}
}

func TestSession_Touch_ExtendsIdleTTLAndLastSeen(t *testing.T) {
	s := authModel.Session{ExpiresAt: sessionNow.Add(time.Hour), LastSeen: sessionNow.Add(-time.Hour)}

	s.Touch(idleTTL, sessionNow)

	if !s.ExpiresAt.Equal(sessionNow.Add(idleTTL)) {
		t.Fatalf("ExpiresAt = %v, want now+idleTTL", s.ExpiresAt)
	}
	if !s.LastSeen.Equal(sessionNow) {
		t.Fatalf("LastSeen = %v, want now", s.LastSeen)
	}
}

func TestSession_BelongsTo(t *testing.T) {
	owner := uuid.Must(uuid.NewV7())
	s := authModel.Session{UserID: owner}
	if !s.BelongsTo(owner) {
		t.Fatal("owner must match")
	}
	if s.BelongsTo(uuid.Must(uuid.NewV7())) {
		t.Fatal("stranger must not match")
	}
}

// L6: a session ends a fixed time after sign-in however busy it is.
func TestSession_ExceedsAbsoluteLifetime(t *testing.T) {
	created := sessionNow
	s := authModel.Session{CreatedAt: created, ExpiresAt: created.Add(1000 * time.Hour)}
	abs := 720 * time.Hour
	if s.ExceedsAbsoluteLifetime(abs, created.Add(abs-time.Second)) {
		t.Fatal("inside the absolute lifetime")
	}
	if !s.ExceedsAbsoluteLifetime(abs, created.Add(abs)) {
		t.Fatal("at the absolute lifetime the session is over, idle deadline or not")
	}
	if s.ExceedsAbsoluteLifetime(0, created.Add(10*abs)) {
		t.Fatal("zero means no absolute limit")
	}
}

func TestSessionIP_KeepsOnlyPublicAddresses(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// public: kept, in canonical form
		{"203.0.113.5", "203.0.113.5"},
		{" 8.8.8.8 ", "8.8.8.8"},
		{"2001:4860:4860::8888", "2001:4860:4860::8888"},
		{"2001:4860:4860:0:0:0:0:8888", "2001:4860:4860::8888"},
		{"::ffff:8.8.4.4", "8.8.4.4"},
		// private, CGNAT, loopback, link-local, unspecified, multicast: an edge or a pod, not the client
		{"10.64.3.2", ""},
		{"172.16.0.1", ""},
		{"172.31.255.255", ""},
		{"192.168.1.10", ""},
		{"100.64.0.1", ""},
		{"100.127.255.254", ""},
		{"127.0.0.1", ""},
		{"169.254.10.1", ""},
		{"0.0.0.0", ""},
		{"224.0.0.1", ""},
		{"::1", ""},
		{"fc00::1", ""},
		{"fd12:3456::1", ""},
		{"fe80::1", ""},
		{"::", ""},
		{"::ffff:10.0.0.1", ""},
		// not an address
		{"", ""},
		{"unknown", ""},
		{"203.0.113.5:443", ""},
		{"fe80::1%eth0", ""},
	}
	for _, c := range cases {
		if got := authModel.SessionIP(c.in); got != c.want {
			t.Errorf("SessionIP(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// just outside the CGNAT and 172.16/12 blocks: public
	for _, ip := range []string{"100.63.255.255", "100.128.0.1", "172.15.255.255", "172.32.0.1"} {
		if got := authModel.SessionIP(ip); got != ip {
			t.Errorf("SessionIP(%q) = %q, want it kept", ip, got)
		}
	}
}

func TestNewSession_StoresOnlyAPublicClientAddress(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	pod := authModel.NewSession(
		userID,
		authModel.SessionMetadata{UserAgent: "ua", IP: "10.64.3.2"},
		idleTTL,
		sessionNow,
	)
	if pod.Metadata.IP != "" || pod.Metadata.UserAgent != "ua" {
		t.Fatalf("a private address must not be stored: %+v", pod.Metadata)
	}
	client := authModel.NewSession(
		userID,
		authModel.SessionMetadata{IP: "::ffff:203.0.113.5"},
		idleTTL,
		sessionNow,
	)
	if client.Metadata.IP != "203.0.113.5" {
		t.Fatalf("a public address is stored canonical: %+v", client.Metadata)
	}
}
