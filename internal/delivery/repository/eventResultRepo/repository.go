package eventResultRepo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type ChangeKind string

const (
	ChangeTeamChallengeSolved    ChangeKind = "team_challenge_solved"
	ChangeTeamChallengeUnsolved  ChangeKind = "team_challenge_unsolved"
	ChangeScoreboardRecalculated ChangeKind = "scoreboard_recalculated"
)

type Change struct {
	EventID   uuid.UUID
	Revision  int64
	Kind      ChangeKind
	Payload   json.RawMessage
	CreatedAt time.Time
}

type Revision struct {
	Revision  int64
	UpdatedAt time.Time
}

type Queries interface {
	AdvanceEventResultRevision(context.Context, postgres.AdvanceEventResultRevisionParams) (postgres.AdvanceEventResultRevisionRow, error)
	CreateEventResultChange(context.Context, postgres.CreateEventResultChangeParams) (postgres.EventResultChange, error)
	DeleteEventResultChangesBefore(context.Context, time.Time) (int64, error)
	GetEarliestEventResultChangeRevision(context.Context, uuid.UUID) (int64, error)
	GetEventResultRevision(context.Context, uuid.UUID) (postgres.GetEventResultRevisionRow, error)
	ListEventResultChangesAfter(context.Context, postgres.ListEventResultChangesAfterParams) ([]postgres.EventResultChange, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) CurrentRevision(ctx context.Context, eventID uuid.UUID) (Revision, error) {
	row, err := r.q.GetEventResultRevision(ctx, eventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Revision{}, nil
		}
		return Revision{}, err
	}
	return Revision{Revision: row.Revision, UpdatedAt: row.UpdatedAt}, nil
}

func (r *Repository) EarliestChangeRevision(ctx context.Context, eventID uuid.UUID) (int64, error) {
	return r.q.GetEarliestEventResultChangeRevision(ctx, eventID)
}

func (r *Repository) DeleteChangesBefore(ctx context.Context, before time.Time) (int64, error) {
	return r.q.DeleteEventResultChangesBefore(ctx, before)
}

func (r *Repository) ListChangesAfter(ctx context.Context, eventID uuid.UUID, after int64, limit int32) ([]Change, error) {
	rows, err := r.q.ListEventResultChangesAfter(ctx, postgres.ListEventResultChangesAfterParams{
		EventID: eventID, AfterRevision: after, LimitVal: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(rows))
	for _, row := range rows {
		out = append(out, Change{
			EventID: row.EventID, Revision: row.Revision, Kind: ChangeKind(row.Kind), Payload: row.Payload, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

// Advance obtains the next event-local revision and stores the associated
// change through the same query executor. Callers pass a transaction-scoped
// executor when the score projection and its change must commit together.
func (r *Repository) Advance(ctx context.Context, eventID uuid.UUID, change Change) (Change, error) {
	revision, err := r.q.AdvanceEventResultRevision(ctx, postgres.AdvanceEventResultRevisionParams{
		EventID: eventID, UpdatedAt: change.CreatedAt,
	})
	if err != nil {
		return Change{}, err
	}
	row, err := r.q.CreateEventResultChange(ctx, postgres.CreateEventResultChangeParams{
		EventID: eventID, Revision: revision.Revision, Kind: string(change.Kind), Payload: change.Payload, CreatedAt: change.CreatedAt,
	})
	if err != nil {
		return Change{}, err
	}
	return Change{EventID: row.EventID, Revision: row.Revision, Kind: ChangeKind(row.Kind), Payload: row.Payload, CreatedAt: row.CreatedAt}, nil
}
