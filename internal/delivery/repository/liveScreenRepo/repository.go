// Package liveScreenRepo stores the live screens of an event (their layouts
// and drafts, see displays.go) and their screen links (hashed tokens). A
// screen has at most one unrevoked link: Issue revokes any previous one and
// inserts the new one in one statement; Revoke is a narrow set operation;
// ActiveTokenHashes is a query-side read for the screen request limiter.
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
	ListActiveLiveScreenTokenHashes(context.Context, pgtype.Timestamptz) ([]postgres.ListActiveLiveScreenTokenHashesRow, error)
	ListActiveEventLiveScreenLinks(context.Context, postgres.ListActiveEventLiveScreenLinksParams) ([]postgres.EventLiveScreenLink, error)
	ListEventLiveScreens(context.Context, uuid.UUID) ([]postgres.EventLiveScreen, error)
	GetEventLiveScreen(context.Context, postgres.GetEventLiveScreenParams) (postgres.EventLiveScreen, error)
	GetFirstEventLiveScreen(context.Context, uuid.UUID) (postgres.EventLiveScreen, error)
	LockEventForLiveScreenChange(context.Context, uuid.UUID) (uuid.UUID, error)
	CountEventLiveScreens(context.Context, uuid.UUID) (int64, error)
	CreateEventLiveScreen(context.Context, postgres.CreateEventLiveScreenParams) (postgres.EventLiveScreen, error)
	RenameEventLiveScreen(context.Context, postgres.RenameEventLiveScreenParams) (postgres.EventLiveScreen, error)
	SetEventLiveScreenPosition(context.Context, postgres.SetEventLiveScreenPositionParams) (int64, error)
	SaveEventLiveScreenDraft(context.Context, postgres.SaveEventLiveScreenDraftParams) (int64, error)
	PublishEventLiveScreen(context.Context, postgres.PublishEventLiveScreenParams) ([]byte, error)
	DeleteEventLiveScreen(context.Context, postgres.DeleteEventLiveScreenParams) (int64, error)
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
	return eventContentModel.LiveScreenLink{ID: row.ID, EventID: row.EventID, ScreenID: row.ScreenID, TokenHash: row.TokenHash, CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy.UUID,
		ExpiresAt: optionalTime(row.ExpiresAt), RevokedAt: optionalTime(row.RevokedAt)}
}

// Active is the screen's working link; pgx.ErrNoRows when there is none.
func (r *Repository) Active(ctx context.Context, eventID, screenID uuid.UUID, now time.Time) (eventContentModel.LiveScreenLink, error) {
	row, err := r.q.GetActiveEventLiveScreenLink(ctx, postgres.GetActiveEventLiveScreenLinkParams{EventID: eventID, ScreenID: screenID, Now: pgtype.Timestamptz{Time: now, Valid: true}})
	if err != nil {
		return eventContentModel.LiveScreenLink{}, err
	}
	return fromDB(row), nil
}

// ActiveByScreen maps each screen of the event to its working link.
func (r *Repository) ActiveByScreen(ctx context.Context, eventID uuid.UUID, now time.Time) (map[uuid.UUID]eventContentModel.LiveScreenLink, error) {
	rows, err := r.q.ListActiveEventLiveScreenLinks(ctx, postgres.ListActiveEventLiveScreenLinksParams{EventID: eventID, Now: pgtype.Timestamptz{Time: now, Valid: true}})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]eventContentModel.LiveScreenLink, len(rows))
	for _, row := range rows {
		out[row.ScreenID] = fromDB(row)
	}
	return out, nil
}

func (r *Repository) GetByTokenHash(ctx context.Context, hash []byte) (eventContentModel.LiveScreenLink, error) {
	row, err := r.q.GetEventLiveScreenLinkByTokenHash(ctx, hash)
	if err != nil {
		return eventContentModel.LiveScreenLink{}, err
	}
	return fromDB(row), nil
}

// Issue stores link as its screen's only working link.
func (r *Repository) Issue(ctx context.Context, link eventContentModel.LiveScreenLink) error {
	return r.q.IssueEventLiveScreenLink(ctx, postgres.IssueEventLiveScreenLinkParams{
		ID: link.ID, EventID: link.EventID, ScreenID: link.ScreenID, TokenHash: link.TokenHash, CreatedAt: link.CreatedAt,
		CreatedBy: uuid.NullUUID{UUID: link.CreatedBy, Valid: link.CreatedBy != uuid.Nil}, ExpiresAt: timestamp(link.ExpiresAt),
	})
}

// Revoke leaves the screen without a working link.
func (r *Repository) Revoke(ctx context.Context, eventID, screenID uuid.UUID, now time.Time) (int64, error) {
	return r.q.RevokeEventLiveScreenLinks(ctx, postgres.RevokeEventLiveScreenLinksParams{EventID: eventID, ScreenID: screenID, RevokedAt: pgtype.Timestamptz{Time: now, Valid: true}})
}

// ActiveTokenHashes maps the token hash of every working link to its expiry
// (nil = none): a read-side shape for the screen request limiter.
func (r *Repository) ActiveTokenHashes(ctx context.Context, now time.Time) (map[string]*time.Time, error) {
	rows, err := r.q.ListActiveLiveScreenTokenHashes(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make(map[string]*time.Time, len(rows))
	for _, row := range rows {
		out[string(row.TokenHash)] = optionalTime(row.ExpiresAt)
	}
	return out, nil
}
