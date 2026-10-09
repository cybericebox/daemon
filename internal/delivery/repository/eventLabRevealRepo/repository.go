package eventLabRevealRepo

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Queries interface {
	InvalidateEventLabRevealBarrier(context.Context, postgres.InvalidateEventLabRevealBarrierParams) error
	GetEventLabRevealBarrier(context.Context, postgres.GetEventLabRevealBarrierParams) (postgres.EventLabRevealBarrier, error)
	ListRevealSetLabs(context.Context, postgres.ListRevealSetLabsParams) ([]postgres.EventTeamLab, error)
	FreezeEventLabRevealBarrier(context.Context, postgres.FreezeEventLabRevealBarrierParams) ([]uuid.UUID, error)
	ListRetryableEmptyEventLabRevealBarriers(context.Context, uuid.UUID) ([]uuid.UUID, error)
	OpenReadyEventLabRevealBarriers(context.Context, postgres.OpenReadyEventLabRevealBarriersParams) (int64, error)
	IsEventLabManualReachable(context.Context, postgres.IsEventLabManualReachableParams) (bool, error)
}
type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q} }
func (r *Repository) Freeze(ctx context.Context, eventID, setID uuid.UUID, now time.Time) ([]uuid.UUID, error) {
	return r.q.FreezeEventLabRevealBarrier(ctx, postgres.FreezeEventLabRevealBarrierParams{EventID: eventID, EventExerciseID: setID, Now: now})
}

func (r *Repository) RetryableEmpty(ctx context.Context, eventID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.ListRetryableEmptyEventLabRevealBarriers(ctx, eventID)
}
func (r *Repository) OpenReady(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	_, err := r.q.OpenReadyEventLabRevealBarriers(ctx, postgres.OpenReadyEventLabRevealBarriersParams{EventID: eventID, Now: pgtype.Timestamptz{Time: now, Valid: true}})
	return err
}
func (r *Repository) Reachable(ctx context.Context, labID uuid.UUID, now time.Time) (bool, error) {
	return r.q.IsEventLabManualReachable(ctx, postgres.IsEventLabManualReachableParams{LabID: labID, Now: now})
}

func (r *Repository) Barrier(ctx context.Context, eventID, setID uuid.UUID) (postgres.EventLabRevealBarrier, error) {
	return r.q.GetEventLabRevealBarrier(ctx, postgres.GetEventLabRevealBarrierParams{EventID: eventID, EventExerciseID: setID})
}
func (r *Repository) Labs(ctx context.Context, eventID, setID uuid.UUID, teams []uuid.UUID) ([]eventLabModel.Lab, error) {
	rows, err := r.q.ListRevealSetLabs(ctx, postgres.ListRevealSetLabsParams{EventID: eventID, EventExerciseID: setID, TeamIds: teams})
	if err != nil {
		return nil, err
	}
	out := make([]eventLabModel.Lab, 0, len(rows))
	for _, row := range rows {
		l, e := eventLabRepo.ToDomain(row)
		if e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, nil
}

func (r *Repository) Invalidate(ctx context.Context, eventID, setID uuid.UUID) error {
	return r.q.InvalidateEventLabRevealBarrier(ctx, postgres.InvalidateEventLabRevealBarrierParams{EventID: eventID, EventExerciseID: setID})
}
