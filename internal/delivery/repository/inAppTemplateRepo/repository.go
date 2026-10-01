// Package inAppTemplateRepo is the repository for the in-app template version
// family: domain shapes in and out, sqlc rows and pgtype only inside.
//
// Publish and Rollback stay single CTE statements on purpose: the version
// family's invariants are enforced atomically in SQL — see the note on
// notificationModel.TemplateStatus.
package inAppTemplateRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	"github.com/cybericebox/daemon/pkg/tools"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateInAppTemplate(ctx context.Context, arg postgres.CreateInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	UpdateInAppTemplate(ctx context.Context, arg postgres.UpdateInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	GetInAppTemplate(ctx context.Context, id uuid.UUID) (postgres.NotificationInAppTemplate, error)
	GetPublishedInAppTemplate(ctx context.Context, arg postgres.GetPublishedInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	DeleteInAppTemplate(ctx context.Context, id uuid.UUID) (int64, error)
	ListInAppTemplates(ctx context.Context, arg postgres.ListInAppTemplatesParams) ([]postgres.NotificationInAppTemplate, error)
	PublishInAppTemplate(ctx context.Context, arg postgres.PublishInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	RollbackInAppTemplate(ctx context.Context, arg postgres.RollbackInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	DeleteEventInAppTemplatesOfType(ctx context.Context, arg postgres.DeleteEventInAppTemplatesOfTypeParams) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// CreateDraft persists a domain-built draft (id + status from NewDraft).
func (r *Repository) CreateDraft(ctx context.Context, t inAppModel.InAppTemplate) (inAppModel.InAppTemplate, error) {
	row, err := r.q.CreateInAppTemplate(ctx, postgres.CreateInAppTemplateParams{
		ID:               t.ID,
		NotificationType: t.NotificationType,
		Status:           string(t.Status),
		Title:            t.Title,
		Body:             t.Body,
		Link:             t.Link,
		Icon:             t.Icon,
		Tone:             t.Tone,
		AccentColor:      t.AccentColor,
		Surface:          t.Surface,
		AutoDismissMs:    nullableInt4(t.AutoDismissMs),
		Actions:          []byte(t.Actions),
		Dismissible:      t.Dismissible,
		UpdatedByUserID:  nullableUUID(t.UpdatedByUserID),
		ScopeEventID:     nullableUUID(t.ScopeEventID),
	})
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	return toDomain(row), nil
}

// UpdateDraft rewrites a draft's content (the WHERE status='draft' guard
// lives in SQL). Not-found propagates raw for the caller to classify.
func (r *Repository) UpdateDraft(ctx context.Context, in inAppModel.UpdateTemplateInput) (inAppModel.InAppTemplate, error) {
	row, err := r.q.UpdateInAppTemplate(ctx, postgres.UpdateInAppTemplateParams{
		ID:              in.ID,
		Title:           in.Title,
		Body:            in.Body,
		Link:            in.Link,
		Icon:            in.Icon,
		Tone:            in.Tone,
		AccentColor:     in.AccentColor,
		Surface:         in.Surface,
		AutoDismissMs:   nullableInt4(in.AutoDismissMs),
		Actions:         []byte(in.Actions),
		Dismissible:     in.Dismissible,
		UpdatedByUserID: nullableUUID(&in.UpdatedBy),
	})
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	return toDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (inAppModel.InAppTemplate, error) {
	row, err := r.q.GetInAppTemplate(ctx, id)
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	return toDomain(row), nil
}

func (r *Repository) GetPublishedByType(ctx context.Context, notificationType string, scopeEventID *uuid.UUID) (inAppModel.InAppTemplate, error) {
	row, err := r.q.GetPublishedInAppTemplate(ctx, postgres.GetPublishedInAppTemplateParams{
		NotificationType: notificationType,
		ScopeEventID:     nullableUUID(scopeEventID),
	})
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	return toDomain(row), nil
}

// DeleteDraft removes a draft (SQL-guarded); returns rows affected.
func (r *Repository) DeleteDraft(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.DeleteInAppTemplate(ctx, id)
}

// DeleteEventType removes every row (any status) of notificationType owned by
// eventID — the reset of an Event override; returns rows affected.
func (r *Repository) DeleteEventType(ctx context.Context, eventID uuid.UUID, notificationType string) (int64, error) {
	return r.q.DeleteEventInAppTemplatesOfType(ctx, postgres.DeleteEventInAppTemplatesOfTypeParams{
		ScopeEventID:     eventID,
		NotificationType: notificationType,
	})
}

func (r *Repository) List(ctx context.Context, typeFilter string, statusFilter notificationModel.TemplateStatus, scopeEventID *uuid.UUID) ([]inAppModel.InAppTemplate, error) {
	rows, err := r.q.ListInAppTemplates(ctx, postgres.ListInAppTemplatesParams{
		TypeFilter:   typeFilter,
		StatusFilter: string(statusFilter),
		ScopeEventID: nullableUUID(scopeEventID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]inAppModel.InAppTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

// Publish promotes a draft (and demotes the current published) atomically.
func (r *Repository) Publish(ctx context.Context, id, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error) {
	row, err := r.q.PublishInAppTemplate(ctx, postgres.PublishInAppTemplateParams{
		ID:              id,
		UpdatedByUserID: nullableUUID(&updatedBy),
	})
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	return toDomain(row), nil
}

// Rollback copies a published or unpublished version into a draft, replacing
// an existing draft atomically. A new ID is used only when no draft exists.
func (r *Repository) Rollback(ctx context.Context, sourceID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error) {
	row, err := r.q.RollbackInAppTemplate(ctx, postgres.RollbackInAppTemplateParams{
		NewID:           tools.NewUUIDv7(),
		SourceID:        sourceID,
		UpdatedByUserID: nullableUUID(&updatedBy),
	})
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	return toDomain(row), nil
}

func toDomain(row postgres.NotificationInAppTemplate) inAppModel.InAppTemplate {
	return inAppModel.InAppTemplate{
		ID:               row.ID,
		ScopeEventID:     nullUUIDPtr(row.ScopeEventID),
		NotificationType: row.NotificationType,
		Status:           notificationModel.TemplateStatus(row.Status),
		Title:            row.Title,
		Body:             row.Body,
		Link:             row.Link,
		Icon:             row.Icon,
		Tone:             row.Tone,
		AccentColor:      row.AccentColor,
		Surface:          row.Surface,
		AutoDismissMs:    nullInt4Ptr(row.AutoDismissMs),
		Actions:          row.Actions,
		Dismissible:      row.Dismissible,
		PublishedAt:      nullTimePtr(row.PublishedAt),
		UpdatedByUserID:  nullUUIDPtr(row.UpdatedByUserID),
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func nullableUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil || *id == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func nullableInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func nullInt4Ptr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return new(v.Int32)
}

func nullTimePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return new(v.Time)
}

func nullUUIDPtr(v uuid.NullUUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	return new(v.UUID)
}
