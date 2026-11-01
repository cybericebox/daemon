// Package googleLinkTicketRepo keeps the pending Google links: a Google identity that signed in with the verified
// address of an existing account and waits for the owner's confirmation. Only hashes of the ticket and of the mailed
// code are stored.
package googleLinkTicketRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	ConsumeGoogleLinkTicket(ctx context.Context, arg postgres.ConsumeGoogleLinkTicketParams) (int64, error)
	CountGoogleLinkTicketCodeAttempt(ctx context.Context, arg postgres.CountGoogleLinkTicketCodeAttemptParams) (int32, error)
	DeleteExpiredGoogleLinkTickets(ctx context.Context, now time.Time) error
	DeleteGoogleLinkTicket(ctx context.Context, id uuid.UUID) (int64, error)
	GetGoogleLinkTicket(ctx context.Context, arg postgres.GetGoogleLinkTicketParams) (postgres.GoogleLinkTicket, error)
	ReplaceGoogleLinkTicket(ctx context.Context, arg postgres.ReplaceGoogleLinkTicketParams) error
	SetGoogleLinkTicketCode(ctx context.Context, arg postgres.SetGoogleLinkTicketCodeParams) (int64, error)
}

// Ticket is a pending Google link.
type Ticket struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	GoogleID     string
	GoogleEmail  string
	GoogleName   string
	ReturnTo     string
	CodeHash     string
	CodeSentAt   *time.Time
	CodeAttempts int32
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Create stores a ticket for (account, Google identity), replacing an earlier one, and drops the expired ones.
func (r *Repository) Create(ctx context.Context, t Ticket, ticketHash string) error {
	_ = r.q.DeleteExpiredGoogleLinkTickets(ctx, t.CreatedAt)
	return r.q.ReplaceGoogleLinkTicket(ctx, postgres.ReplaceGoogleLinkTicketParams{
		ID: t.ID, TicketHash: ticketHash, UserID: t.UserID, GoogleID: t.GoogleID, GoogleEmail: t.GoogleEmail, GoogleName: t.GoogleName, ReturnTo: t.ReturnTo,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
	})
}

// Get loads a live ticket by the hash of its value; the raw not-found error is the caller's to classify.
func (r *Repository) Get(ctx context.Context, ticketHash string, now time.Time) (Ticket, error) {
	row, err := r.q.GetGoogleLinkTicket(ctx, postgres.GetGoogleLinkTicketParams{TicketHash: ticketHash, Now: now})
	if err != nil {
		return Ticket{}, err
	}
	t := Ticket{ID: row.ID, UserID: row.UserID, GoogleID: row.GoogleID, GoogleEmail: row.GoogleEmail, GoogleName: row.GoogleName, ReturnTo: row.ReturnTo, CodeHash: row.CodeHash,
		CodeAttempts: row.CodeAttempts, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
	if row.CodeSentAt.Valid {
		sent := row.CodeSentAt.Time
		t.CodeSentAt = &sent
	}
	return t, nil
}

// SetCode keeps the hash of a newly mailed code (all attempts again); false when the ticket is gone.
func (r *Repository) SetCode(ctx context.Context, id uuid.UUID, codeHash string, sentAt time.Time) (bool, error) {
	n, err := r.q.SetGoogleLinkTicketCode(ctx, postgres.SetGoogleLinkTicketCodeParams{ID: id, CodeHash: codeHash, SentAt: pgtype.Timestamptz{Time: sentAt, Valid: true}})
	return n > 0, err
}

// CountCodeAttempt counts one check of the mailed code; false when no attempts are left (or the ticket is gone).
func (r *Repository) CountCodeAttempt(ctx context.Context, id uuid.UUID, now time.Time, max int32) (bool, error) {
	_, err := r.q.CountGoogleLinkTicketCodeAttempt(ctx, postgres.CountGoogleLinkTicketCodeAttemptParams{ID: id, Now: now, MaxAttempts: max})
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Consume claims the ticket: true for exactly one of any number of concurrent callers.
func (r *Repository) Consume(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	n, err := r.q.ConsumeGoogleLinkTicket(ctx, postgres.ConsumeGoogleLinkTicketParams{ID: id, Now: now})
	return n > 0, err
}

// Delete drops the ticket (the user gave the link up, or its attempts ran out).
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.q.DeleteGoogleLinkTicket(ctx, id)
	return err
}
