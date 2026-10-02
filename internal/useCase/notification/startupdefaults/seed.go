// Package startupdefaults creates the settings and default templates of the notification types that
// have no migration (emaildefaults.StartupSeeded for email, inappdefaults.StartupSeeded for in-app).
package startupdefaults

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/notification/emaildefaults"
	"github.com/cybericebox/daemon/internal/model/notification/inappdefaults"
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
	GetPublishedInAppTemplate(ctx context.Context, arg postgres.GetPublishedInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	CreateInAppTemplate(ctx context.Context, arg postgres.CreateInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
	PublishInAppTemplate(ctx context.Context, arg postgres.PublishInAppTemplateParams) (postgres.NotificationInAppTemplate, error)
}

// inAppLook is the icon and tone the in-app templates of the startup-seeded types get (the migration-seeded
// types of the same kind use the same ones).
var inAppLook = map[string][2]string{
	"exercise.elevation.requested": {"mail", "info"},
	"exercise.elevation.approved":  {"success", "success"},
	"exercise.elevation.rejected":  {"warning", "warning"},
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
	return seedInApp(ctx, q)
}

// seedInApp creates the published platform in-app template of every startup-seeded in-app type that has none.
// A template an admin edited or replaced is left alone.
func seedInApp(ctx context.Context, q Queries) error {
	for _, tpl := range inappdefaults.All(inappdefaults.UK) {
		if !inappdefaults.StartupSeeded[tpl.Type] {
			continue
		}
		if _, err := q.GetPublishedInAppTemplate(ctx, postgres.GetPublishedInAppTemplateParams{NotificationType: tpl.Type}); err == nil {
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read the %s in-app template: %w", tpl.Type, err)
		}
		look := inAppLook[tpl.Type]
		draft, err := q.CreateInAppTemplate(ctx, postgres.CreateInAppTemplateParams{
			ID: uuid.Must(uuid.NewV7()), NotificationType: tpl.Type, Status: "draft", Title: tpl.Title, Body: tpl.Body, Link: tpl.Link,
			Icon: look[0], Tone: look[1], Surface: "inbox", Actions: []byte("[]"), Dismissible: true,
		})
		if err != nil {
			return fmt.Errorf("seed the %s in-app template: %w", tpl.Type, err)
		}
		if _, err = q.PublishInAppTemplate(ctx, postgres.PublishInAppTemplateParams{ID: draft.ID}); err != nil {
			return fmt.Errorf("publish the %s in-app template: %w", tpl.Type, err)
		}
	}
	return nil
}
