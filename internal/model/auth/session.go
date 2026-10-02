package authModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// Session represents a session stored in the database.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	LastSeen  time.Time
	CreatedAt time.Time
	Metadata  SessionMetadata
}

type SessionMetadata struct {
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
}

// NewSession builds a fresh session with domain-owned defaults: the ID
// (UUIDv7) and every timestamp are set here, not in the database — one source
// of truth, and the entity is fully testable without a DB. now is passed in so
// callers (and tests) control the clock.
func NewSession(userID uuid.UUID, meta SessionMetadata, idleTTL time.Duration, now time.Time) Session {
	return Session{
		ID:        uuid.Must(uuid.NewV7()),
		UserID:    userID,
		ExpiresAt: now.Add(idleTTL),
		LastSeen:  now,
		CreatedAt: now,
		Metadata:  meta,
	}
}

// IsExpired reports whether the session's idle deadline has passed.
func (s *Session) IsExpired(now time.Time) bool {
	return now.After(s.ExpiresAt)
}

// ExceedsAbsoluteLifetime reports whether the session is older than absolute (a
// session ends that long after sign-in however busy it is); zero means no limit.
func (s *Session) ExceedsAbsoluteLifetime(absolute time.Duration, now time.Time) bool {
	return absolute > 0 && !now.Before(s.CreatedAt.Add(absolute))
}

// Touch records activity: it extends the idle deadline and moves LastSeen.
func (s *Session) Touch(idleTTL time.Duration, now time.Time) {
	s.ExpiresAt = now.Add(idleTTL)
	s.LastSeen = now
}

// BelongsTo reports whether the session is owned by userID.
func (s *Session) BelongsTo(userID uuid.UUID) bool {
	return s.UserID == userID
}
