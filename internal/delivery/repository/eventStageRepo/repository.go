// Package eventStageRepo maps event stages to PostgreSQL.
package eventStageRepo

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

type Queries interface {
	CreateEventStage(ctx context.Context, arg postgres.CreateEventStageParams) (postgres.EventStage, error)
	GetEventStage(ctx context.Context, arg postgres.GetEventStageParams) (postgres.EventStage, error)
	ListEventStages(ctx context.Context, eventID uuid.UUID) ([]postgres.EventStage, error)
	UpdateEventStage(ctx context.Context, arg postgres.UpdateEventStageParams) (postgres.EventStage, error)
	DeleteEventStage(ctx context.Context, arg postgres.DeleteEventStageParams) (int64, error)
	CountEventStageSets(ctx context.Context, stageID uuid.NullUUID) (int32, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, s eventModel.Stage) (eventModel.Stage, error) {
	row, err := r.q.CreateEventStage(ctx, postgres.CreateEventStageParams{LabRetentionMinutes: minutes(s.LabRetentionMinutes), ID: s.ID, EventID: s.EventID, Name: s.Name, OpensAt: s.OpensAt,
		ClosesAt: s.ClosesAt, Returnable: s.Returnable, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	if err != nil {
		return eventModel.Stage{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Get(ctx context.Context, eventID, id uuid.UUID) (eventModel.Stage, error) {
	row, err := r.q.GetEventStage(ctx, postgres.GetEventStageParams{ID: id, EventID: eventID})
	if err != nil {
		return eventModel.Stage{}, err
	}
	return ToDomain(row), nil
}

// List returns the event's stages in time order.
func (r *Repository) List(ctx context.Context, eventID uuid.UUID) ([]eventModel.Stage, error) {
	rows, err := r.q.ListEventStages(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]eventModel.Stage, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

func (r *Repository) Update(ctx context.Context, s eventModel.Stage) (eventModel.Stage, error) {
	row, err := r.q.UpdateEventStage(ctx, postgres.UpdateEventStageParams{LabRetentionMinutes: minutes(s.LabRetentionMinutes), ID: s.ID, EventID: s.EventID, Name: s.Name, OpensAt: s.OpensAt,
		ClosesAt: s.ClosesAt, Returnable: s.Returnable, UpdatedAt: s.UpdatedAt})
	if err != nil {
		return eventModel.Stage{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Delete(ctx context.Context, eventID, id uuid.UUID) (int64, error) {
	return r.q.DeleteEventStage(ctx, postgres.DeleteEventStageParams{ID: id, EventID: eventID})
}

// CountSets is how many sets still point at the stage.
func (r *Repository) CountSets(ctx context.Context, id uuid.UUID) (int, error) {
	n, err := r.q.CountEventStageSets(ctx, uuid.NullUUID{UUID: id, Valid: true})
	return int(n), err
}

func ToDomain(row postgres.EventStage) eventModel.Stage {
	var retention *int32
	if row.LabRetentionMinutes.Valid {
		n := row.LabRetentionMinutes.Int32
		retention = &n
	}
	return eventModel.Stage{LabRetentionMinutes: retention, ID: row.ID, EventID: row.EventID, Name: row.Name, OpensAt: row.OpensAt, ClosesAt: row.ClosesAt,
		Returnable: row.Returnable, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func minutes(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}
