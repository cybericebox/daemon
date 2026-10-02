// Package startupdefaults creates the settings and default templates of the notification types that
// have no migration (emaildefaults.StartupSeeded).
package startupdefaults

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
)

// startupSeededTypes are the notification types whose settings and default email template are
// created at start from the code-owned defaults, not by a migration (the migration-seeded types
// predate this). Idempotent: a template an admin edited or replaced is left alone.
var startupSeededTypes = emaildefaults.StartupSeeded

type Queries interface {
	GetNotificationSetting(ctx context.Context, arg postgres.GetNotificationSettingParams) (postgres.NotificationSetting, error)
	UpsertNotificationSetting(ctx context.Context, arg postgres.UpsertNotificationSettingParams) (postgres.NotificationSetting, error)
	GetPublishedEmailTemplate(ctx context.Context, arg postgres.GetPublishedEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	CreateEmailTemplate(ctx context.Context, arg postgres.CreateEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
	PublishEmailTemplate(ctx context.Context, arg postgres.PublishEmailTemplateParams) (postgres.NotificationEmailTemplate, error)
}

// Seed makes sure every startup-seeded type is delivered by email (a security
// notice: always on, not user-changeable) and has a published platform template.
func Seed(ctx context.Context, q Queries) error {
	for _, tpl := range emaildefaults.All(emaildefaults.UK) {
		if !startupSeededTypes[tpl.Type] {
			continue
		}
		if _, err := q.GetNotificationSetting(ctx, postgres.GetNotificationSettingParams{NotificationType: tpl.Type, Channel: "email"}); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("read the %s setting: %w", tpl.Type, err)
			}
			if _, err = q.UpsertNotificationSetting(ctx, postgres.UpsertNotificationSettingParams{
				NotificationType: tpl.Type, Channel: "email", Enabled: true, UserCanChange: false, UserDefault: true,
			}); err != nil {
				return fmt.Errorf("seed the %s setting: %w", tpl.Type, err)
			}
		}
		if _, err := q.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{NotificationType: tpl.Type}); err == nil {
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read the %s template: %w", tpl.Type, err)
		}
		draft, err := q.CreateEmailTemplate(ctx, postgres.CreateEmailTemplateParams{
			ID: uuid.Must(uuid.NewV7()), NotificationType: tpl.Type, Status: "draft",
			Subject: tpl.Subject, Preheader: tpl.Preheader, Body: []byte(tpl.Body), Styling: []byte("{}"),
		})
		if err != nil {
			return fmt.Errorf("seed the %s template: %w", tpl.Type, err)
		}
		if _, err = q.PublishEmailTemplate(ctx, postgres.PublishEmailTemplateParams{ID: draft.ID}); err != nil {
			return fmt.Errorf("publish the %s template: %w", tpl.Type, err)
		}
	}
	return nil
}
