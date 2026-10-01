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
