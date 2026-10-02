package inAppUseCase

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/inAppTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/notificationSettingsRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

type NotificationInAppTemplateUseCase struct {
	templates *inAppTemplateRepo.Repository
	settings  *notificationSettingsRepo.Repository
}

// repoPort composes the two aggregate-repository query slices this use case
// wraps; *postgres.Queries satisfies it structurally.
type repoPort interface {
	inAppTemplateRepo.Queries
	notificationSettingsRepo.Queries
}

func NewNotificationInAppTemplateUseCase(repo repoPort) *NotificationInAppTemplateUseCase {
	return &NotificationInAppTemplateUseCase{
		templates: inAppTemplateRepo.New(repo),
		settings:  notificationSettingsRepo.New(repo),
	}
}

func (u *NotificationInAppTemplateUseCase) CreateInAppTemplate(
	ctx context.Context,
	in inAppModel.CreateTemplateInput,
) (inAppModel.InAppTemplate, error) {
	if err := validateInAppTemplate(notificationTypes.NotificationType(in.NotificationType), in.Title, in.Body, in.Link, in.Actions); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	created, err := u.templates.CreateDraft(ctx, inAppModel.NewDraft(in))
	if err != nil {
		return inAppModel.InAppTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to create in-app template").
			Err()
	}
	return created, nil
}

// GetInAppTemplate returns a single in-app template by id, or ErrTemplateNotFound when absent.
func (u *NotificationInAppTemplateUseCase) GetInAppTemplate(
	ctx context.Context,
	id uuid.UUID,
) (inAppModel.InAppTemplate, error) {
	return u.platformTemplate(ctx, id)
}

// platformTemplate loads a platform (non-Event) template by id. Event-scoped
// rows belong to the Event API and are reported as not found here, so every
// by-id platform operation is confined to the platform family.
func (u *NotificationInAppTemplateUseCase) platformTemplate(ctx context.Context, id uuid.UUID) (inAppModel.InAppTemplate, error) {
	tpl, err := u.templates.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return inAppModel.InAppTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get in-app template").
			Err()
	}
	if tpl.ScopeEventID != nil {
		return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return tpl, nil
}

func (u *NotificationInAppTemplateUseCase) UpdateInAppTemplate(
	ctx context.Context,
	in inAppModel.UpdateTemplateInput,
) (inAppModel.InAppTemplate, error) {
	existing, err := u.platformTemplate(ctx, in.ID)
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	if err := validateInAppTemplate(notificationTypes.NotificationType(existing.NotificationType), in.Title, in.Body, in.Link, in.Actions); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	tpl, err := u.templates.UpdateDraft(ctx, in)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return inAppModel.InAppTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to update in-app template").
			Err()
	}
	return tpl, nil
}

func (u *NotificationInAppTemplateUseCase) DeleteInAppTemplate(
	ctx context.Context,
	id uuid.UUID,
) error {
	if _, err := u.platformTemplate(ctx, id); err != nil {
		return err
	}
	n, err := u.templates.DeleteDraft(ctx, id)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete in-app template").Err()
	}
	if n == 0 {
		return notificationModel.ErrTemplateNotFound.Err()
	}
	return nil
}

func (u *NotificationInAppTemplateUseCase) ListInAppTemplates(
	ctx context.Context,
	filter inAppModel.ListFilter,
) (inAppModel.ListResult, error) {
	templates, err := u.templates.List(ctx, filter.Type, notificationModel.TemplateStatus(filter.Status), filter.ScopeEventID)
	if err != nil {
		return inAppModel.ListResult{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list in-app templates").
			Err()
	}
	settings, err := u.settings.ListGlobal(ctx)
	if err != nil {
		return inAppModel.ListResult{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list notification settings").
			Err()
	}
	inAppEnabled := make(map[string]bool)
	for _, s := range settings {
		if s.Channel == string(notificationTypes.NotificationChannelInApp) && s.Enabled {
			inAppEnabled[s.NotificationType] = true
		}
	}
	hasPublished := make(map[string]bool)
	for _, t := range templates {
		if t.IsPublished() {
			hasPublished[t.NotificationType] = true
		}
	}
	var missing []string
	for typ := range inAppEnabled {
		if !hasPublished[typ] {
			missing = append(missing, typ)
		}
	}
	return inAppModel.ListResult{
		Templates:        templates,
		MissingActiveFor: missing,
	}, nil
}

func (u *NotificationInAppTemplateUseCase) PublishInAppTemplate(
	ctx context.Context, id, updatedBy uuid.UUID,
) (inAppModel.InAppTemplate, error) {
	template, err := u.platformTemplate(ctx, id)
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	if err := validateInAppTemplate(notificationTypes.NotificationType(template.NotificationType), template.Title, template.Body, template.Link, template.Actions); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	tpl, err := u.templates.Publish(ctx, id, updatedBy)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return inAppModel.InAppTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to publish in-app template").
			Err()
	}
	return tpl, nil
}

func validateInAppTemplate(typ notificationTypes.NotificationType, title, body, link string, actions json.RawMessage) error {
	if err := notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelInApp, title, body, link); err != nil {
		return notificationModel.ErrInvalidTemplateVariables.WithError(err).Err()
	}
	if !inAppModel.ValidLink(link, true) || !inAppModel.ValidActions(actions, true) {
		return notificationModel.ErrTemplateLinkInvalid.Err()
	}
	return nil
}

func (u *NotificationInAppTemplateUseCase) RollbackInAppTemplate(
	ctx context.Context, sourceID, updatedBy uuid.UUID,
) (inAppModel.InAppTemplate, error) {
	if _, err := u.platformTemplate(ctx, sourceID); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	tpl, err := u.templates.Rollback(ctx, sourceID, updatedBy)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return inAppModel.InAppTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to rollback in-app template").
			Err()
	}
	return tpl, nil
}

// LatestInAppTemplates returns one TypeVersions entry per notification type,
// folding all versions into Draft / Published / Unpublished slots.
func (u *NotificationInAppTemplateUseCase) LatestInAppTemplates(
	ctx context.Context,
) ([]inAppModel.TypeVersions, error) {
	templates, err := u.templates.List(ctx, "", "", nil)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list in-app templates").
			Err()
	}

	index := make(map[string]*inAppModel.TypeVersions)
	for _, kind := range notificationTypes.Types() {
		if notificationTypes.Supports(kind.Type, notificationTypes.NotificationChannelInApp) {
			index[string(kind.Type)] = &inAppModel.TypeVersions{NotificationType: string(kind.Type)}
		}
	}
	for _, t := range templates {
		entry, ok := index[t.NotificationType]
		if !ok {
			entry = &inAppModel.TypeVersions{NotificationType: t.NotificationType}
			index[t.NotificationType] = entry
		}
		switch {
		case t.IsDraft():
			entry.Draft = new(t)
		case t.IsPublished():
			entry.Published = new(t)
		case t.IsUnpublished():
			entry.Unpublished = new(t)
		}
	}

	out := make([]inAppModel.TypeVersions, 0, len(index))
	for _, v := range index {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NotificationType < out[j].NotificationType })
	return out, nil
}
