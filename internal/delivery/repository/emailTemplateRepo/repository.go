// Package emailTemplateRepo is the repository for the email template version
// family and its block presets: domain shapes in and out, sqlc rows and
// pgtype only inside.
//
// Publish and Rollback stay single CTE statements on purpose: the version
// family's invariants (one published per type, publish only from draft,
// rollback only from a saved published or unpublished version) are enforced atomically in SQL — see the
// note on notificationModel.TemplateStatus.
package emailTemplateRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/pkg/tools"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateEmailTemplate(ctx context.Context, arg postgres.CreateEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	UpdateEmailTemplate(ctx context.Context, arg postgres.UpdateEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	GetEmailTemplate(ctx context.Context, id uuid.UUID) (postgres.NotificationEmailTemplate, error)
	GetPublishedEmailTemplate(ctx context.Context, arg postgres.GetPublishedEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	DeleteEmailTemplate(ctx context.Context, id uuid.UUID) (int64, error)
	ListEmailTemplates(ctx context.Context, arg postgres.ListEmailTemplatesParams) ([]postgres.NotificationEmailTemplate, error)
	PublishEmailTemplate(ctx context.Context, arg postgres.PublishEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	RollbackEmailTemplate(ctx context.Context, arg postgres.RollbackEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	DeleteEventEmailTemplatesOfType(ctx context.Context, arg postgres.DeleteEventEmailTemplatesOfTypeParams) ([]uuid.UUID, error)
	EmailTemplateFileUsableByEvent(ctx context.Context, arg postgres.EmailTemplateFileUsableByEventParams) (bool, error)
	ListEmailBlockPresets(ctx context.Context) ([]postgres.NotificationEmailBlockPreset, error)
	GetEmailBlockPreset(ctx context.Context, id uuid.UUID) (postgres.NotificationEmailBlockPreset, error)
	CreateEmailBlockPreset(ctx context.Context, arg postgres.CreateEmailBlockPresetParams) (postgres.NotificationEmailBlockPreset, error)
	UpdateEmailBlockPreset(ctx context.Context, arg postgres.UpdateEmailBlockPresetParams) (postgres.NotificationEmailBlockPreset, error)
	DeleteEmailBlockPreset(ctx context.Context, id uuid.UUID) error
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ── templates ───────────────────────────────────────────────────────────────

// CreateDraft persists a domain-built draft (id + status from NewDraft).
func (r *Repository) CreateDraft(ctx context.Context, t emailModel.EmailTemplate) (emailModel.EmailTemplate, error) {
	row, err := r.q.CreateEmailTemplate(ctx, postgres.CreateEmailTemplateParams{
		ID:               t.ID,
		NotificationType: t.NotificationType,
		Status:           string(t.Status),
		Subject:          t.Subject,
		Preheader:        t.Preheader,
		Body:             []byte(t.Body),
		Styling:          []byte(t.Styling),
		UpdatedByUserID:  nullableUUID(t.UpdatedByUserID),
		ScopeEventID:     nullableUUID(t.ScopeEventID),
	})
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return toDomain(row), nil
}

// UpdateDraft rewrites a draft's content (the WHERE status='draft' guard
// lives in SQL). Not-found propagates raw for the caller to classify.
func (r *Repository) UpdateDraft(ctx context.Context, in emailModel.UpdateTemplateInput) (emailModel.EmailTemplate, error) {
	row, err := r.q.UpdateEmailTemplate(ctx, postgres.UpdateEmailTemplateParams{
		ID:              in.ID,
		Subject:         in.Subject,
		Preheader:       in.Preheader,
		Body:            []byte(in.Body),
		Styling:         []byte(in.Styling),
		UpdatedByUserID: nullableUUID(&in.UpdatedBy),
	})
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return toDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (emailModel.EmailTemplate, error) {
	row, err := r.q.GetEmailTemplate(ctx, id)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return toDomain(row), nil
}

func (r *Repository) GetPublishedByType(ctx context.Context, notificationType string, scopeEventID *uuid.UUID) (emailModel.EmailTemplate, error) {
	row, err := r.q.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{
		NotificationType: notificationType,
		ScopeEventID:     nullableUUID(scopeEventID),
	})
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return toDomain(row), nil
}

// DeleteDraft removes a draft (SQL-guarded); returns rows affected.
func (r *Repository) DeleteDraft(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.DeleteEmailTemplate(ctx, id)
}

// DeleteEventType removes every row (any status) of notificationType owned by
// eventID — the reset of an Event override — and returns the deleted ids.
// A set operation, not an aggregate mutation: deleting the whole version
// family keeps it trivially consistent.
func (r *Repository) DeleteEventType(ctx context.Context, eventID uuid.UUID, notificationType string) ([]uuid.UUID, error) {
	return r.q.DeleteEventEmailTemplatesOfType(ctx, postgres.DeleteEventEmailTemplatesOfTypeParams{
		ScopeEventID:     eventID,
		NotificationType: notificationType,
	})
}

// FileUsableByEvent reports whether fileID is referenced by a platform
// template (scope NULL), a block preset, or a template row of eventID.
func (r *Repository) FileUsableByEvent(ctx context.Context, fileID, eventID uuid.UUID) (bool, error) {
	return r.q.EmailTemplateFileUsableByEvent(ctx, postgres.EmailTemplateFileUsableByEventParams{
		TemplateRefType: mediaModel.RefTypeEmailTemplate,
		PresetRefType:   mediaModel.RefTypeEmailBlockPreset,
		FileID:          fileID,
		EventID:         eventID,
	})
}

func (r *Repository) List(ctx context.Context, typeFilter string, statusFilter notificationModel.TemplateStatus, scopeEventID *uuid.UUID) ([]emailModel.EmailTemplate, error) {
	rows, err := r.q.ListEmailTemplates(ctx, postgres.ListEmailTemplatesParams{
		TypeFilter:   typeFilter,
		StatusFilter: string(statusFilter),
		ScopeEventID: nullableUUID(scopeEventID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]emailModel.EmailTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

// Publish promotes a draft (and demotes the current published) atomically.
func (r *Repository) Publish(ctx context.Context, id, updatedBy uuid.UUID) (emailModel.EmailTemplate, error) {
	row, err := r.q.PublishEmailTemplate(ctx, postgres.PublishEmailTemplateParams{
		ID:              id,
		UpdatedByUserID: nullableUUID(&updatedBy),
	})
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return toDomain(row), nil
}

// Rollback copies a published or unpublished version into a draft, replacing
// an existing draft atomically. A new ID is used only when no draft exists.
func (r *Repository) Rollback(ctx context.Context, sourceID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error) {
	row, err := r.q.RollbackEmailTemplate(ctx, postgres.RollbackEmailTemplateParams{
		NewID:           tools.NewUUIDv7(),
		SourceID:        sourceID,
		UpdatedByUserID: nullableUUID(&updatedBy),
	})
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return toDomain(row), nil
}

// ── block presets ───────────────────────────────────────────────────────────

func (r *Repository) ListPresets(ctx context.Context) ([]emailModel.BlockPreset, error) {
	rows, err := r.q.ListEmailBlockPresets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]emailModel.BlockPreset, 0, len(rows))
	for _, row := range rows {
		out = append(out, presetToDomain(row))
	}
	return out, nil
}

func (r *Repository) GetPreset(ctx context.Context, id uuid.UUID) (emailModel.BlockPreset, error) {
	row, err := r.q.GetEmailBlockPreset(ctx, id)
	if err != nil {
		return emailModel.BlockPreset{}, err
	}
	return presetToDomain(row), nil
}

func (r *Repository) CreatePreset(ctx context.Context, in emailModel.PresetInput) (emailModel.BlockPreset, error) {
	row, err := r.q.CreateEmailBlockPreset(ctx, postgres.CreateEmailBlockPresetParams{
		ID:          tools.NewUUIDv7(),
		Name:        in.Name,
		Description: in.Description,
		Blocks:      []byte(in.Blocks),
	})
	if err != nil {
		return emailModel.BlockPreset{}, err
	}
	return presetToDomain(row), nil
}

func (r *Repository) UpdatePreset(ctx context.Context, id uuid.UUID, in emailModel.PresetInput) (emailModel.BlockPreset, error) {
	row, err := r.q.UpdateEmailBlockPreset(ctx, postgres.UpdateEmailBlockPresetParams{
		ID:          id,
		Name:        in.Name,
		Description: in.Description,
		Blocks:      []byte(in.Blocks),
	})
	if err != nil {
		return emailModel.BlockPreset{}, err
	}
	return presetToDomain(row), nil
}

func (r *Repository) DeletePreset(ctx context.Context, id uuid.UUID) error {
	return r.q.DeleteEmailBlockPreset(ctx, id)
}

// ── mapping ─────────────────────────────────────────────────────────────────

func toDomain(row postgres.NotificationEmailTemplate) emailModel.EmailTemplate {
	return emailModel.EmailTemplate{
		ID:               row.ID,
		ScopeEventID:     nullUUIDPtr(row.ScopeEventID),
		NotificationType: row.NotificationType,
		Status:           notificationModel.TemplateStatus(row.Status),
		Subject:          row.Subject,
		Preheader:        row.Preheader,
		Body:             row.Body,
		Styling:          row.Styling,
		PublishedAt:      nullTimePtr(row.PublishedAt),
		UpdatedByUserID:  nullUUIDPtr(row.UpdatedByUserID),
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func presetToDomain(row postgres.NotificationEmailBlockPreset) emailModel.BlockPreset {
	return emailModel.BlockPreset{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Blocks:      row.Blocks,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func nullableUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil || *id == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
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
