package broadcastUseCase

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// Broadcast templates are saved broadcast messages of one scope (scopeEventID
// nil = the platform). They are not bound to any platform action and never
// send anything by themselves: the composer loads one into its form and the
// broadcast is sent as usual. Access is decided by the routes (the broadcast
// permission on the platform, Event manage/read access in an Event); here every
// call only stays inside its scope.

// ListBroadcastTemplates lists the scope's templates, recently edited first.
func (u *NotificationBroadcastUseCase) ListBroadcastTemplates(ctx context.Context, scopeEventID *uuid.UUID) ([]broadcastModel.Template, error) {
	items, err := u.templates.ListTemplates(ctx, scopeEventID, broadcastModel.MaxTemplates)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list broadcast templates").Err()
	}
	return items, nil
}

// GetBroadcastTemplate loads one template of the scope.
func (u *NotificationBroadcastUseCase) GetBroadcastTemplate(ctx context.Context, scopeEventID *uuid.UUID, id uuid.UUID) (broadcastModel.Template, error) {
	t, err := u.templates.GetTemplate(ctx, id, scopeEventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return broadcastModel.Template{}, notificationModel.ErrBroadcastTemplateNotFound.Err()
		}
		return broadcastModel.Template{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the broadcast template").Err()
	}
	return t, nil
}

// CreateBroadcastTemplate validates and stores a new template.
func (u *NotificationBroadcastUseCase) CreateBroadcastTemplate(ctx context.Context, scopeEventID *uuid.UUID, actor uuid.UUID, in broadcastModel.TemplateInput) (broadcastModel.Template, error) {
	in, err := u.prepareTemplate(ctx, scopeEventID, actor, in)
	if err != nil {
		return broadcastModel.Template{}, err
	}
	id := uuid.Must(uuid.NewV7())
	if err = u.templates.CreateTemplate(ctx, id, scopeEventID, actor, in); err != nil {
		return broadcastModel.Template{}, templateWriteError(err, "Failed to store the broadcast template")
	}
	return u.GetBroadcastTemplate(ctx, scopeEventID, id)
}

// UpdateBroadcastTemplate replaces the name and the content of a template of the scope.
func (u *NotificationBroadcastUseCase) UpdateBroadcastTemplate(ctx context.Context, scopeEventID *uuid.UUID, id, actor uuid.UUID, in broadcastModel.TemplateInput) (broadcastModel.Template, error) {
	in, err := u.prepareTemplate(ctx, scopeEventID, actor, in)
	if err != nil {
		return broadcastModel.Template{}, err
	}
	if err = u.templates.UpdateTemplate(ctx, id, scopeEventID, actor, in); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return broadcastModel.Template{}, notificationModel.ErrBroadcastTemplateNotFound.Err()
		}
		return broadcastModel.Template{}, templateWriteError(err, "Failed to update the broadcast template")
	}
	return u.GetBroadcastTemplate(ctx, scopeEventID, id)
}

// DeleteBroadcastTemplate removes a template of the scope. Broadcasts already
// sent from it keep their own copy of the message.
func (u *NotificationBroadcastUseCase) DeleteBroadcastTemplate(ctx context.Context, scopeEventID *uuid.UUID, id uuid.UUID) error {
	if err := u.templates.DeleteTemplate(ctx, id, scopeEventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return notificationModel.ErrBroadcastTemplateNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete the broadcast template").Err()
	}
	return nil
}

// prepareTemplate trims the input, keeps only the parts of the chosen channels
// and checks it by the rules of a sent broadcast (images included), so a
// template always fills a message that can be sent.
func (u *NotificationBroadcastUseCase) prepareTemplate(ctx context.Context, scopeEventID *uuid.UUID, actor uuid.UUID, in broadcastModel.TemplateInput) (broadcastModel.TemplateInput, error) {
	in = in.Normalized()
	c := in.Content
	if !c.HasChannel(notificationTypes.NotificationChannelEmail) {
		c.Subject, c.Preheader, c.EmailBody, c.EmailStyling = "", "", json.RawMessage("[]"), json.RawMessage("{}")
	}
	if !c.HasChannel(notificationTypes.NotificationChannelInApp) {
		c.InAppTitle, c.InAppBody, c.InAppLink = "", "", ""
	}
	in.Content = c
	if err := in.Validate(); err != nil {
		return in, err
	}
	if err := u.checkContentImages(ctx, scopeEventID, actor, in.Content); err != nil {
		return in, err
	}
	return in, nil
}

func templateWriteError(err error, message string) error {
	if _, ok := repositoryTools.UniqueViolationError(err, notificationModel.ErrBroadcastTemplateNameTaken); ok {
		return notificationModel.ErrBroadcastTemplateNameTaken.Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage(message).Err()
}
