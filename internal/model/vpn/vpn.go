// Package vpnModel is the domain for per-user VPN access records: a WireGuard
// config issued to a user within a scope (an event, or a standalone test), stored
// encrypted. It owns the scope vocabulary and the record factory; the ciphertext
// itself is produced by the application layer.
package vpnModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// Scope discriminates why a user holds a VPN config, so the same user can hold
// several at once (one per event, plus a test one) and each is found by its
// provenance.
type Scope string

const (
	// ScopeEvent: a participant's VPN within an event; ScopeRef is the event id.
	ScopeEvent Scope = "event"
	// ScopeTest: a moderator/admin standing a variant up for testing, outside any
	// event; ScopeRef is the test deploy id (each deploy has its own client key).
	ScopeTest Scope = "test"
)

func (s Scope) Valid() bool {
	return s == ScopeEvent || s == ScopeTest
}

// Config is one user's VPN config for a scope. Config holds ciphertext at rest.
type Config struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Scope     Scope
	ScopeRef  uuid.NullUUID // event id for ScopeEvent; deploy id for ScopeTest
	Config    string        // encrypted WireGuard config
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewConfig builds a config record for storage. now is a parameter so callers
// (and tests) control the clock; the id is a fresh UUIDv7.
func NewConfig(userID uuid.UUID, scope Scope, scopeRef uuid.NullUUID, ciphertext string, now time.Time) Config {
	return Config{
		ID:        uuid.Must(uuid.NewV7()),
		UserID:    userID,
		Scope:     scope,
		ScopeRef:  scopeRef,
		Config:    ciphertext,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
