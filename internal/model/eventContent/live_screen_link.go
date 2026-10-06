package eventContentModel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/gofrs/uuid"
)

// LiveScreenLink lets a projector or LED PC that is not signed in open this
// event's live screen: read-only, the staff view of the published layout.
// An event has at most one working link; issuing a new one revokes the old.
// Only the SHA-256 of the token is stored; the token itself is shown once.
type LiveScreenLink struct {
	ID        uuid.UUID
	EventID   uuid.UUID
	TokenHash []byte
	CreatedAt time.Time
	CreatedBy uuid.UUID
	// ExpiresAt nil = «Без обмеження».
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

const (
	liveScreenTokenBytes = 32
)

// LiveScreenLinkMaxTTL caps «until the event ends» for long events
// (LIVE_SCREEN_LINK_MAX_TTL, set once at start).
var LiveScreenLinkMaxTTL = 60 * 24 * time.Hour

// LiveScreenExpiry is the organizer's choice of how long a link works.
type LiveScreenExpiry string

const (
	LiveScreenExpiryNone     LiveScreenExpiry = "none"
	LiveScreenExpiryDay      LiveScreenExpiry = "day"
	LiveScreenExpiryWeek     LiveScreenExpiry = "week"
	LiveScreenExpiryEventEnd LiveScreenExpiry = "event_end"
)

// liveScreenExpiresAt resolves the choice; «until the event ends» needs a
// future finish and is capped at LiveScreenLinkMaxTTL.
func liveScreenExpiresAt(expiry LiveScreenExpiry, now time.Time, finish *time.Time) (*time.Time, bool) {
	at := func(value time.Time) (*time.Time, bool) { return &value, true }
	switch expiry {
	case LiveScreenExpiryNone:
		return nil, true
	case LiveScreenExpiryDay:
		return at(now.Add(24 * time.Hour))
	case LiveScreenExpiryWeek:
		return at(now.Add(7 * 24 * time.Hour))
	case LiveScreenExpiryEventEnd:
		if finish == nil || !finish.After(now) {
			return nil, false
		}
		if limit := now.Add(LiveScreenLinkMaxTTL); finish.After(limit) {
			return at(limit)
		}
		return at(*finish)
	default:
		return nil, false
	}
}

// NewLiveScreenLink issues a link for the chosen expiry (finish is the
// event's effective finish) and returns it with its token.
func NewLiveScreenLink(eventID, by uuid.UUID, now time.Time, expiry LiveScreenExpiry, finish *time.Time) (LiveScreenLink, string, error) {
	expiresAt, ok := liveScreenExpiresAt(expiry, now, finish)
	if !ok {
		return LiveScreenLink{}, "", ErrLiveScreenLinkExpiryInvalid.Err()
	}
	return issueLiveScreenLink(eventID, by, now, expiresAt)
}

// Regenerate issues a new token for the same event and expiry; the caller
// stores it together with revoking this link.
func (l *LiveScreenLink) Regenerate(by uuid.UUID, now time.Time) (LiveScreenLink, string, error) {
	return issueLiveScreenLink(l.EventID, by, now, l.ExpiresAt)
}

func issueLiveScreenLink(eventID, by uuid.UUID, now time.Time, expiresAt *time.Time) (LiveScreenLink, string, error) {
	raw := make([]byte, liveScreenTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return LiveScreenLink{}, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return LiveScreenLink{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return LiveScreenLink{ID: id, EventID: eventID, TokenHash: HashLiveScreenToken(token), CreatedAt: now, CreatedBy: by, ExpiresAt: expiresAt}, token, nil
}

// HashLiveScreenToken is the stored form of a token.
func HashLiveScreenToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// LiveScreenTokenWellFormed rejects obviously bad tokens before any lookup.
func LiveScreenTokenWellFormed(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == liveScreenTokenBytes
}

// Active reports whether the link still opens the screen.
func (l *LiveScreenLink) Active(now time.Time) bool {
	return l.RevokedAt == nil && (l.ExpiresAt == nil || now.Before(*l.ExpiresAt))
}

// Revoke ends the link at once («Відкликати» or a regeneration).
func (l *LiveScreenLink) Revoke(now time.Time) {
	if l.RevokedAt == nil {
		at := now
		l.RevokedAt = &at
	}
}
