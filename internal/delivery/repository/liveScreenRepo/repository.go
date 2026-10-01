// Package liveScreenRepo stores the event's screen link (hashed token). An
// event has at most one unrevoked link: Issue revokes any previous one and
// inserts the new one in one statement; Revoke is a narrow set operation.
package liveScreenRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

type Queries interface {
	GetActiveEventLiveScreenLink(context.Context, postgres.GetActiveEventLiveScreenLinkParams) (postgres.EventLiveScreenLink, error)
	GetEventLiveScreenLinkByTokenHash(context.Context, []byte) (postgres.EventLiveScreenLink, error)
	IssueEventLiveScreenLink(context.Context, postgres.IssueEventLiveScreenLinkParams) error
	RevokeEventLiveScreenLinks(context.Context, postgres.RevokeEventLiveScreenLinksParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func timestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}

func fromDB(row postgres.EventLiveScreenLink) eventContentModel.LiveScreenLink {
	return eventContentModel.LiveScreenLink{ID: row.ID, EventID: row.EventID, TokenHash: row.TokenHash, CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy.UUID,
		ExpiresAt: optionalTime(row.ExpiresAt), RevokedAt: optionalTime(row.RevokedAt)}
}

// Active is the event's working link; pgx.ErrNoRows when there is none.
func (r *Repository) Active(ctx context.Context, eventID uuid.UUID, now time.Time) (eventContentModel.LiveScreenLink, error) {
	row, err := r.q.GetActiveEventLiveScreenLink(ctx, postgres.GetActiveEventLiveScreenLinkParams{EventID: eventID, Now: pgtype.Timestamptz{Time: now, Valid: true}})
	if err != nil {
		return eventContentModel.LiveScreenLink{}, err
	}
	return fromDB(row), nil
}

func (r *Repository) GetByTokenHash(ctx context.Context, hash []byte) (eventContentModel.LiveScreenLink, error) {
	row, err := r.q.GetEventLiveScreenLinkByTokenHash(ctx, hash)
	if err != nil {
		return eventContentModel.LiveScreenLink{}, err
	}
	return fromDB(row), nil
}

// Issue stores link as the event's only working link.
func (r *Repository) Issue(ctx context.Context, link eventContentModel.LiveScreenLink) error {
	return r.q.IssueEventLiveScreenLink(ctx, postgres.IssueEventLiveScreenLinkParams{
		ID: link.ID, EventID: link.EventID, TokenHash: link.TokenHash, CreatedAt: link.CreatedAt,
		CreatedBy: uuid.NullUUID{UUID: link.CreatedBy, Valid: link.CreatedBy != uuid.Nil}, ExpiresAt: timestamp(link.ExpiresAt),
	})
}

// Revoke leaves the event without a working link.
func (r *Repository) Revoke(ctx context.Context, eventID uuid.UUID, now time.Time) (int64, error) {
	return r.q.RevokeEventLiveScreenLinks(ctx, postgres.RevokeEventLiveScreenLinksParams{EventID: eventID, RevokedAt: pgtype.Timestamptz{Time: now, Valid: true}})
}
