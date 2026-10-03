// Package session holds the machinery of the session cookie: the encrypted ticket the cookie carries, the
// in-memory set of revoked sessions every replica keeps, and the batching of last_seen writes. None of it
// touches the database on the request path.
package session

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/pkg/secret"
)

var (
	// ErrInvalidTicket is a cookie that is not a ticket of this platform: garbage, forged, or sealed with a key
	// that is gone.
	ErrInvalidTicket = errors.New("session: invalid ticket")
	// ErrExpiredTicket is a genuine ticket past its expiry.
	ErrExpiredTicket = errors.New("session: ticket expired")
)

// ticketContext is authenticated with every ticket, so a ciphertext of another secret family never opens here.
var ticketContext = []byte("cybericebox/session-ticket/v1")

// ticketSize is the plain size of an encoded ticket: two UUIDs and three unix times.
const ticketSize = 16 + 16 + 3*8

// Ticket is what the session cookie holds. No role and no name: the rights of the caller are read from the
// user row on the requests that need them.
type Ticket struct {
	SessionID  uuid.UUID
	UserID     uuid.UUID
	SignedInAt time.Time
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

// Lifetimes are the two TTLs of a session.
type Lifetimes struct {
	// Idle is how long a cookie lives after it was issued; a use re-issues it.
	Idle time.Duration
	// Absolute ends a session this long after sign-in however busy it is; zero means no limit.
	Absolute time.Duration
}

// NewTicket issues the ticket of a session that was just opened.
func NewTicket(sessionID, userID uuid.UUID, now time.Time, l Lifetimes) Ticket {
	return Ticket{SessionID: sessionID, UserID: userID, SignedInAt: now, IssuedAt: now, ExpiresAt: l.expiry(now, now)}
}

// expiry is the time of issue plus the idle TTL, capped by sign-in plus the absolute TTL.
func (l Lifetimes) expiry(signedIn, issued time.Time) time.Time {
	exp := issued.Add(l.Idle)
	if l.Absolute > 0 {
		if limit := signedIn.Add(l.Absolute); limit.Before(exp) {
			exp = limit
		}
	}
	return exp
}

// Expired reports whether the ticket is past its expiry.
func (t Ticket) Expired(now time.Time) bool { return !now.Before(t.ExpiresAt) }

// ReissueDue reports whether 1% of the idle TTL has passed since the cookie was issued (7.2 min at 12h).
func (t Ticket) ReissueDue(now time.Time, l Lifetimes) bool {
	return now.Sub(t.IssuedAt) >= l.Idle/100
}

// Reissue is the same session with a new time of issue and a new expiry.
func (t Ticket) Reissue(now time.Time, l Lifetimes) Ticket {
	t.IssuedAt = now
	t.ExpiresAt = l.expiry(t.SignedInAt, now)
	return t
}

// Codec seals and opens tickets (AES-GCM through the secret keyring: the cookie is encrypted and authenticated).
type Codec struct {
	sealer secret.Sealer
}

func NewCodec(sealer secret.Sealer) *Codec { return &Codec{sealer: sealer} }

// Seal returns the cookie value of a ticket.
func (c *Codec) Seal(t Ticket) (string, error) {
	plain := make([]byte, 0, ticketSize)
	plain = append(plain, t.SessionID.Bytes()...)
	plain = append(plain, t.UserID.Bytes()...)
	plain = binary.BigEndian.AppendUint64(plain, uint64(t.SignedInAt.Unix()))
	plain = binary.BigEndian.AppendUint64(plain, uint64(t.IssuedAt.Unix()))
	plain = binary.BigEndian.AppendUint64(plain, uint64(t.ExpiresAt.Unix()))
	return c.sealer.EncryptWithContext(plain, ticketContext)
}

// Open decrypts a cookie value and checks its expiry; no database is involved. A garbage cookie is
// ErrInvalidTicket, a genuine but expired one ErrExpiredTicket.
func (c *Codec) Open(value string, now time.Time) (Ticket, error) {
	plain, err := c.sealer.DecryptWithContext(value, ticketContext)
	if err != nil || len(plain) != ticketSize {
		return Ticket{}, ErrInvalidTicket
	}
	t := Ticket{
		SessionID:  uuid.FromBytesOrNil(plain[0:16]),
		UserID:     uuid.FromBytesOrNil(plain[16:32]),
		SignedInAt: time.Unix(int64(binary.BigEndian.Uint64(plain[32:40])), 0),
		IssuedAt:   time.Unix(int64(binary.BigEndian.Uint64(plain[40:48])), 0),
		ExpiresAt:  time.Unix(int64(binary.BigEndian.Uint64(plain[48:56])), 0),
	}
	if t.SessionID == uuid.Nil || t.UserID == uuid.Nil {
		return Ticket{}, ErrInvalidTicket
	}
	if t.Expired(now) {
		return Ticket{}, ErrExpiredTicket
	}
	return t, nil
}
