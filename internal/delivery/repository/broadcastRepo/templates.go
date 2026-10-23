package broadcastRepo

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// TemplateQueries is the slice of the sqlc Querier the broadcast templates need.
type TemplateQueries interface {
	CreateNotificationBroadcastTemplate(ctx context.Context, arg postgres.CreateNotificationBroadcastTemplateParams) (uuid.UUID, error)
	UpdateNotificationBroadcastTemplate(ctx context.Context, arg postgres.UpdateNotificationBroadcastTemplateParams) (int64, error)
	DeleteNotificationBroadcastTemplate(ctx context.Context, arg postgres.DeleteNotificationBroadcastTemplateParams) (int64, error)
	ListNotificationBroadcastTemplates(ctx context.Context, arg postgres.ListNotificationBroadcastTemplatesParams) ([]postgres.ListNotificationBroadcastTemplatesRow, error)
	GetNotificationBroadcastTemplate(ctx context.Context, arg postgres.GetNotificationBroadcastTemplateParams) (postgres.GetNotificationBroadcastTemplateRow, error)
}

// TemplateRepository stores broadcast templates. Every call is limited to one
// scope (nil = the platform): a template of another scope is not found.
type TemplateRepository struct {
	q TemplateQueries
}

func NewTemplates(q TemplateQueries) *TemplateRepository { return &TemplateRepository{q: q} }

// CreateTemplate stores a new template authored by actor.
func (r *TemplateRepository) CreateTemplate(ctx context.Context, id uuid.UUID, scope *uuid.UUID, actor uuid.UUID, in broadcastModel.TemplateInput) error {
	_, err := r.q.CreateNotificationBroadcastTemplate(ctx, postgres.CreateNotificationBroadcastTemplateParams{
		ID: id, ScopeEventID: nullUUID(scope), Name: in.Name, Channels: channelStrings(in.Content.Channels),
		Subject: in.Content.Subject, Preheader: in.Content.Preheader,
		EmailBody: orDefault(in.Content.EmailBody, "[]"), EmailStyling: orDefault(in.Content.EmailStyling, "{}"),
		InappTitle: in.Content.InAppTitle, InappBody: in.Content.InAppBody, InappLink: in.Content.InAppLink,
		Actor: nullUUID(&actor),
	})
	return err
}

// UpdateTemplate replaces the template's name and content; pgx.ErrNoRows when
// the scope has no such template.
func (r *TemplateRepository) UpdateTemplate(ctx context.Context, id uuid.UUID, scope *uuid.UUID, actor uuid.UUID, in broadcastModel.TemplateInput) error {
	n, err := r.q.UpdateNotificationBroadcastTemplate(ctx, postgres.UpdateNotificationBroadcastTemplateParams{
		ID: id, ScopeFilter: broadcastModel.ScopeFilter(scope), Name: in.Name, Channels: channelStrings(in.Content.Channels),
		Subject: in.Content.Subject, Preheader: in.Content.Preheader,
		EmailBody: orDefault(in.Content.EmailBody, "[]"), EmailStyling: orDefault(in.Content.EmailStyling, "{}"),
		InappTitle: in.Content.InAppTitle, InappBody: in.Content.InAppBody, InappLink: in.Content.InAppLink,
		Actor: nullUUID(&actor),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteTemplate removes the template; pgx.ErrNoRows when the scope has no such template.
func (r *TemplateRepository) DeleteTemplate(ctx context.Context, id uuid.UUID, scope *uuid.UUID) error {
	n, err := r.q.DeleteNotificationBroadcastTemplate(ctx, postgres.DeleteNotificationBroadcastTemplateParams{ID: id, ScopeFilter: broadcastModel.ScopeFilter(scope)})
	if err != nil {
		return err
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetTemplate loads one template of the scope. Not found propagates raw.
func (r *TemplateRepository) GetTemplate(ctx context.Context, id uuid.UUID, scope *uuid.UUID) (broadcastModel.Template, error) {
	row, err := r.q.GetNotificationBroadcastTemplate(ctx, postgres.GetNotificationBroadcastTemplateParams{ID: id, ScopeFilter: broadcastModel.ScopeFilter(scope)})
	if err != nil {
		return broadcastModel.Template{}, err
	}
	return toTemplate(postgres.ListNotificationBroadcastTemplatesRow(row)), nil
}

// ListTemplates returns the scope's templates, recently edited first.
func (r *TemplateRepository) ListTemplates(ctx context.Context, scope *uuid.UUID, limit int32) ([]broadcastModel.Template, error) {
	rows, err := r.q.ListNotificationBroadcastTemplates(ctx, postgres.ListNotificationBroadcastTemplatesParams{ScopeFilter: broadcastModel.ScopeFilter(scope), LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]broadcastModel.Template, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTemplate(row))
	}
	return out, nil
}

func toTemplate(row postgres.ListNotificationBroadcastTemplatesRow) broadcastModel.Template {
	channels := make([]notificationTypes.NotificationChannel, 0, len(row.Channels))
	for _, ch := range row.Channels {
		channels = append(channels, notificationTypes.NotificationChannel(ch))
	}
	t := broadcastModel.Template{
		ID: row.ID, Name: row.Name, UpdatedByName: row.UpdatedByName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Content: broadcastModel.Content{
			Channels: channels, Subject: row.Subject, Preheader: row.Preheader,
			EmailBody: row.EmailBody, EmailStyling: row.EmailStyling,
			InAppTitle: row.InappTitle, InAppBody: row.InappBody, InAppLink: row.InappLink,
		},
	}
	if row.ScopeEventID.Valid {
		t.ScopeEventID = new(row.ScopeEventID.UUID)
	}
	if row.CreatedBy.Valid {
		t.CreatedBy = new(row.CreatedBy.UUID)
	}
	if row.UpdatedBy.Valid {
		t.UpdatedBy = new(row.UpdatedBy.UUID)
	}
	return t
}

func channelStrings(channels []notificationTypes.NotificationChannel) []string {
	out := make([]string, 0, len(channels))
	for _, ch := range channels {
		out = append(out, string(ch))
	}
	return out
}
