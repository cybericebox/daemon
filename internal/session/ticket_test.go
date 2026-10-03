package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/pkg/secret"
)

const keyA = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
const keyB = "ff02030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f00"

var (
	t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	lf = Lifetimes{Idle: 12 * time.Hour, Absolute: 7 * 24 * time.Hour}
)

func codec(t *testing.T, spec string) *Codec {
	t.Helper()
	c, err := secret.New(spec)
	if err != nil {
		t.Fatal(err)
	}
	return NewCodec(c)
}

func newTicket() Ticket {
	return NewTicket(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), t0, lf)
}

func TestTicketRoundTrip(t *testing.T) {
	c := codec(t, keyA)
	in := newTicket()
	value, err := c.Seal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Open(value, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != in.SessionID || out.UserID != in.UserID || !out.SignedInAt.Equal(in.SignedInAt) ||
		!out.IssuedAt.Equal(in.IssuedAt) || !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Fatalf("round trip changed the ticket: %+v vs %+v", out, in)
	}
}

func TestTicketRefusesGarbageForgedAndForeignKeys(t *testing.T) {
	c := codec(t, keyA)
	value, _ := c.Seal(newTicket())
	cases := map[string]string{
		"empty":     "",
		"garbage":   "not-a-ticket",
		"truncated": value[:len(value)-6],
		"tampered":  value[:len(value)-4] + strings.Repeat("A", 4),
		"foreign":   mustSeal(t, codec(t, keyB), newTicket()),
	}
	for name, v := range cases {
		if _, err := c.Open(v, t0); !errors.Is(err, ErrInvalidTicket) {
			t.Errorf("%s: want ErrInvalidTicket, got %v", name, err)
		}
	}
}

func mustSeal(t *testing.T, c *Codec, tk Ticket) string {
	t.Helper()
	v, err := c.Seal(tk)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTicketOfAnotherSecretFamilyDoesNotOpen(t *testing.T) {
	c, _ := secret.New(keyA)
	other, _ := c.EncryptWithContext(make([]byte, ticketSize), []byte("something else"))
	if _, err := NewCodec(c).Open(other, t0); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("want ErrInvalidTicket, got %v", err)
	}
}

func TestTicketKeyRotationKeepsOldCookiesOpen(t *testing.T) {
	old := codec(t, "old:"+keyA)
	value := mustSeal(t, old, newTicket())
	rotated := codec(t, "new:"+keyB+",old:"+keyA)
	if _, err := rotated.Open(value, t0); err != nil {
		t.Fatalf("a cookie of the old key must open after rotation: %v", err)
	}
}

func TestTicketExpiry(t *testing.T) {
	c := codec(t, keyA)
	value := mustSeal(t, c, newTicket())
	if _, err := c.Open(value, t0.Add(12*time.Hour-time.Second)); err != nil {
		t.Fatalf("before the expiry: %v", err)
	}
	if _, err := c.Open(value, t0.Add(12*time.Hour)); !errors.Is(err, ErrExpiredTicket) {
		t.Fatalf("at the expiry: want ErrExpiredTicket, got %v", err)
	}
}

func TestExpiryIsCappedByTheAbsoluteTTL(t *testing.T) {
	tk := NewTicket(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), t0, lf)
	late := t0.Add(7*24*time.Hour - time.Hour)
	re := tk.Reissue(late, lf)
	if want := t0.Add(7 * 24 * time.Hour); !re.ExpiresAt.Equal(want) {
		t.Fatalf("expiry %v, want the cap %v", re.ExpiresAt, want)
	}
	if !re.SignedInAt.Equal(t0) || !re.IssuedAt.Equal(late) {
		t.Fatalf("sign-in time must stay, issue time must move: %+v", re)
	}
	noCap := Lifetimes{Idle: time.Hour}
	if got := NewTicket(uuid.Nil, uuid.Nil, t0, noCap).ExpiresAt; !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("no absolute limit: %v", got)
	}
}

func TestReissueDueAtOnePercentOfTheIdleTTL(t *testing.T) {
	tk := newTicket()
	if tk.ReissueDue(t0.Add(7*time.Minute+11*time.Second), lf) {
		t.Fatal("not due before 7.2 min")
	}
	if !tk.ReissueDue(t0.Add(7*time.Minute+12*time.Second), lf) {
		t.Fatal("due at 7.2 min")
	}
}
