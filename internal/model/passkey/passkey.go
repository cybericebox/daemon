// Package passkeyModel is the passkey entity: a WebAuthn discoverable credential a person signs in with.
package passkeyModel

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"
)

const (
	// MaxPerUser is how many passkeys one account may hold.
	MaxPerUser = 10
	// MaxNameLength is the longest name a person gives a passkey, in characters.
	MaxNameLength = 64
)

// Passkey is the public half of a credential and what the platform knows about it. No biometric data is ever
// received: the authenticator verifies the person locally and only says that it did.
type Passkey struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// CredentialID is the authenticator's id of the key (what the sign-in looks the key up by).
	CredentialID []byte
	PublicKey    []byte
	// SignCount is the authenticator's usage counter; zero throughout for authenticators that keep none.
	SignCount  uint32
	AAGUID     []byte
	Transports []string
	// UserVerified is always true: a passkey is registered only with user verification.
	UserVerified   bool
	BackupEligible bool
	// BackupState reports a key that is synced to the person's other devices.
	BackupState bool
	Name        string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	// CompromisedAt is when the counter went backwards, which means a copy of the key exists. Such a key never
	// signs in again; it stays listed until the person removes it.
	CompromisedAt *time.Time
}

// NormalizeName trims a person-given name and reports whether it is acceptable: 1 to MaxNameLength characters,
// no control characters.
func NormalizeName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", false
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f {
			return "", false
		}
	}
	return name, true
}
