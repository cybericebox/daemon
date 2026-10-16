// Package participantPreviewRepo persists isolated settings and whole owned runs.
package participantPreviewRepo

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	preview "github.com/cybericebox/daemon/internal/model/participantPreview"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Queries interface {
	ListEventPreviewRuns(context.Context, postgres.ListEventPreviewRunsParams) ([]postgres.EventPreviewRun, error)
	GetEventPreviewSettings(context.Context, uuid.UUID) (postgres.GetEventPreviewSettingsRow, error)
	UpsertEventPreviewSettings(context.Context, postgres.UpsertEventPreviewSettingsParams) error
	DeleteEventPreviewSettings(context.Context, uuid.UUID) error
	LockEventPreviewOwner(context.Context, string) error
	GetEventPreviewRun(context.Context, postgres.GetEventPreviewRunParams) (postgres.EventPreviewRun, error)
	UpsertEventPreviewRun(context.Context, postgres.UpsertEventPreviewRunParams) error
	DeleteEventPreviewRun(context.Context, postgres.DeleteEventPreviewRunParams) error
}
type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }
func (r *Repository) Configuration(ctx context.Context, event uuid.UUID) (preview.Settings, error) {
	row, err := r.q.GetEventPreviewSettings(ctx, event)
	if err != nil {
		return preview.Settings{}, err
	}
	out := preview.Settings{Inherit: len(row.Selections) == 0, Selections: []preview.Selection{}}
	if !out.Inherit {
		if err = json.Unmarshal(row.Selections, &out.Selections); err != nil {
			return out, err
		}
	}
	if row.GroupName.Valid {
		name := row.GroupName.String
		out.GroupName = &name
	}
	return out, nil
}
func (r *Repository) Settings(ctx context.Context, event uuid.UUID) ([]preview.Selection, error) {
	config, err := r.Configuration(ctx, event)
	return config.Selections, err
}
func (r *Repository) SaveConfiguration(ctx context.Context, event uuid.UUID, items []preview.Selection, inherit bool, name *string, now time.Time) error {
	var data []byte
	var err error
	if !inherit {
		data, err = json.Marshal(items)
		if err != nil {
			return err
		}
	}
	group := pgtype.Text{}
	if name != nil {
		group = pgtype.Text{String: *name, Valid: true}
	}
	return r.q.UpsertEventPreviewSettings(ctx, postgres.UpsertEventPreviewSettingsParams{EventID: event, Selections: data, GroupName: group, UpdatedAt: now})
}

func (r *Repository) SaveSettings(ctx context.Context, event uuid.UUID, items []preview.Selection, now time.Time) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return r.q.UpsertEventPreviewSettings(ctx, postgres.UpsertEventPreviewSettingsParams{EventID: event, Selections: data, UpdatedAt: now})
}
func (r *Repository) Inherit(ctx context.Context, event uuid.UUID) error {
	return r.q.DeleteEventPreviewSettings(ctx, event)
}
func (r *Repository) Lock(ctx context.Context, event, user uuid.UUID) error {
	return r.q.LockEventPreviewOwner(ctx, event.String()+":"+user.String())
}
func (r *Repository) Get(ctx context.Context, event, user, selection uuid.UUID) (preview.Run, error) {
	row, err := r.q.GetEventPreviewRun(ctx, postgres.GetEventPreviewRunParams{EventID: event, UserID: user, SelectionID: selection})
	if err != nil {
		return preview.Run{}, err
	}
	var out preview.Run
	if err = json.Unmarshal(row.State, &out); err != nil {
		return out, err
	}
	out.ID, out.EventID, out.UserID, out.SelectionID = row.ID, row.EventID, row.UserID, row.SelectionID
	out.DeployID, out.CreatedAt, out.ExpiresAt = row.DeployID, row.CreatedAt, row.ExpiresAt
	return out, nil
}
func (r *Repository) Save(ctx context.Context, run preview.Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	return r.q.UpsertEventPreviewRun(ctx, postgres.UpsertEventPreviewRunParams{ID: run.ID, EventID: run.EventID, UserID: run.UserID, SelectionID: run.SelectionID, DeployID: run.DeployID, State: data, CreatedAt: run.CreatedAt, ExpiresAt: run.ExpiresAt})
}
func (r *Repository) Delete(ctx context.Context, event, user, selection uuid.UUID) error {
	return r.q.DeleteEventPreviewRun(ctx, postgres.DeleteEventPreviewRunParams{EventID: event, UserID: user, SelectionID: selection})
}

func (r *Repository) List(ctx context.Context, event, user uuid.UUID) ([]preview.Run, error) {
	rows, err := r.q.ListEventPreviewRuns(ctx, postgres.ListEventPreviewRunsParams{EventID: event, UserID: user})
	if err != nil {
		return nil, err
	}
	out := make([]preview.Run, 0, len(rows))
	for _, row := range rows {
		var run preview.Run
		if err = json.Unmarshal(row.State, &run); err != nil {
			return nil, err
		}
		run.ID, run.EventID, run.UserID, run.SelectionID = row.ID, row.EventID, row.UserID, row.SelectionID
		run.DeployID, run.CreatedAt, run.ExpiresAt = row.DeployID, row.CreatedAt, row.ExpiresAt
		out = append(out, run)
	}
	return out, nil
}
