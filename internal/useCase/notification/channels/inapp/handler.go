package inAppUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/inAppTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/inboxRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

// handlerPort composes the aggregate-repository query slices the delivery
// handler needs; *postgres.Queries satisfies it structurally.
type handlerPort interface {
	inAppTemplateRepo.Queries
	inboxRepo.Queries
}

type Handler struct {
	templates *inAppTemplateRepo.Repository
	inbox     *inboxRepo.Repository
}

func NewHandler(repo handlerPort) *Handler {
	return &Handler{templates: inAppTemplateRepo.New(repo), inbox: inboxRepo.New(repo)}
}

func (h *Handler) Channel() notificationTypes.NotificationChannel {
	return notificationTypes.NotificationChannelInApp
}

func (h *Handler) Handle(
	ctx context.Context,
	user userModel.User,
	t notificationTypes.NotificationType,
	vars map[string]any,
	templateID *uuid.UUID,
	scopeEventID ...*uuid.UUID,
) error {
	var (
		tmpl inAppModel.InAppTemplate
		err  error
	)
	if bc, ok := dispatchModel.BroadcastFrom(ctx); ok {
		// A broadcast carries its own content instead of a stored template.
		tmpl = bc.AsInAppTemplate()
	} else {
		if templateID != nil {
			tmpl, err = h.templates.GetByID(ctx, *templateID)
		} else {
			tmpl, err = h.templates.GetPublishedByType(ctx, string(t), firstScope(scopeEventID))
		}
		if err != nil {
			if repositoryTools.IsObjectNotFoundError(err) {
				return notificationModel.ErrTemplateNotFound.Err()
			}
			return model.ErrPlatform.WithError(err).WithMessage("Failed to load in-app template").Err()
		}
		if templateID != nil && tmpl.NotificationType != string(t) {
			return notificationModel.ErrTemplateNotFound.Err()
		}
	}
	title, err := render.RenderText(tmpl.Title, vars)
	if err != nil {
		return err
	}
	body, err := render.RenderHTML(tmpl.Body, vars)
	if err != nil {
		return err
	}
	// The server keeps the body to the small in-app format; the browser sanitizes again, but is not the only defence.
	body = inAppModel.SanitizeBody(body)
	link, err := render.RenderText(tmpl.Link, vars)
	if err != nil {
		return err
	}
	// What the variables turned into is checked again: a value may not become a javascript: link.
	if !inAppModel.ValidLink(link, false) {
		link = ""
	}
	return h.inbox.Deliver(ctx, user.ID, inboxRepo.Delivery{
		// The product name never breaks across lines, whatever the stored template says.
		Title: mailModel.KeepBrandText(title), Body: mailModel.KeepBrandText(body), Link: link,
		Icon: tmpl.Icon, Tone: tmpl.Tone, AccentColor: tmpl.AccentColor,
		Surface: tmpl.Surface, AutoDismissMs: inboxDisplayDuration(tmpl.Surface, tmpl.AutoDismissMs),
		Actions: inAppModel.SafeActions(tmpl.Actions), Dismissible: tmpl.Dismissible,
		// The dispatch scope files the item under its Event inbox (M5).
		ScopeEventID: firstScope(scopeEventID),
		Inbox:        inboxMeta(ctx, t),
	})
}

// inboxMeta is the planner's classification of this dispatch; direct sends
// (account mail, previews) are classified for the subject role.
func inboxMeta(ctx context.Context, t notificationTypes.NotificationType) inboxModel.Meta {
	if meta, ok := dispatchModel.InboxMetaFrom(ctx); ok {
		meta.Type = string(t)
		return meta
	}
	return inboxModel.NewMeta(string(t), inboxModel.RoleSubject, "")
}

func inboxDisplayDuration(surface string, configured *int32) *int32 {
	if surface != "inbox" {
		return configured
	}
	duration := int32(5000)
	if configured != nil {
		duration = *configured
	}
	if duration < 3000 {
		duration = 3000
	}
	if duration > 10000 {
		duration = 10000
	}
	return &duration
}

func firstScope(scopes []*uuid.UUID) *uuid.UUID {
	if len(scopes) == 0 {
		return nil
	}
	return scopes[0]
}
