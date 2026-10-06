package emailUseCase

import (
	"context"
	"sort"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/notificationSettingsRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

type NotificationEmailTemplateUseCase struct {
	templates *emailTemplateRepo.Repository
	settings  *notificationSettingsRepo.Repository
	media     TemplateMedia
	images    *TemplateImages
	footers   FooterSource
}

// repoPort composes the two aggregate-repository query slices this use case
// wraps; *postgres.Queries satisfies it structurally.
type repoPort interface {
	emailTemplateRepo.Queries
	notificationSettingsRepo.Queries
}

func NewNotificationEmailTemplateUseCase(repo repoPort, media TemplateMedia) *NotificationEmailTemplateUseCase {
	templates := emailTemplateRepo.New(repo)
	return &NotificationEmailTemplateUseCase{
		templates: templates,
		settings:  notificationSettingsRepo.New(repo),
		media:     media,
		images:    NewTemplateImages(media, templates),
	}
}

func (u *NotificationEmailTemplateUseCase) CreateEmailTemplate(
	ctx context.Context,
	in emailModel.CreateTemplateInput,
) (emailModel.EmailTemplate, error) {
	if err := validateEmailTemplate(notificationTypes.NotificationType(in.NotificationType), in.Subject, in.Preheader, in.Body, in.Styling); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := u.images.ValidateBody(ctx, in.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	created, err := u.templates.CreateDraft(ctx, emailModel.NewDraft(in))
	if err != nil {
		return emailModel.EmailTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to create email template").
			Err()
	}
	if err := u.images.SyncReferences(ctx, created.ID, created.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return created, nil
}

// GetEmailTemplate returns a single email template by id, or ErrTemplateNotFound when absent.
func (u *NotificationEmailTemplateUseCase) GetEmailTemplate(
	ctx context.Context,
	id uuid.UUID,
) (emailModel.EmailTemplate, error) {
	return u.platformTemplate(ctx, id)
}

// platformTemplate loads a platform (non-Event) template by id. Event-scoped
// rows belong to the Event API and are reported as not found here, so every
// by-id platform operation is confined to the platform family.
func (u *NotificationEmailTemplateUseCase) platformTemplate(ctx context.Context, id uuid.UUID) (emailModel.EmailTemplate, error) {
	tpl, err := u.templates.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return emailModel.EmailTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get email template").
			Err()
	}
	if tpl.ScopeEventID != nil {
		return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return tpl, nil
}

func (u *NotificationEmailTemplateUseCase) UpdateEmailTemplate(
	ctx context.Context,
	in emailModel.UpdateTemplateInput,
) (emailModel.EmailTemplate, error) {
	existing, err := u.platformTemplate(ctx, in.ID)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := validateEmailTemplate(notificationTypes.NotificationType(existing.NotificationType), in.Subject, in.Preheader, in.Body, in.Styling); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := u.images.ValidateBody(ctx, in.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	tpl, err := u.templates.UpdateDraft(ctx, in)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return emailModel.EmailTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to update email template").
			Err()
	}
	if err := u.images.SyncReferences(ctx, tpl.ID, tpl.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return tpl, nil
}

func (u *NotificationEmailTemplateUseCase) DeleteEmailTemplate(
	ctx context.Context,
	id uuid.UUID,
) error {
	if _, err := u.platformTemplate(ctx, id); err != nil {
		return err
	}
	n, err := u.templates.DeleteDraft(ctx, id)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete email template").Err()
	}
	if n == 0 {
		return notificationModel.ErrTemplateNotFound.Err()
	}
	return u.images.RemoveReferences(ctx, id)
}

func (u *NotificationEmailTemplateUseCase) ListEmailTemplates(
	ctx context.Context,
	filter emailModel.ListFilter,
) (emailModel.ListResult, error) {
	templates, err := u.templates.List(ctx, filter.Type, notificationModel.TemplateStatus(filter.Status), filter.ScopeEventID)
	if err != nil {
		return emailModel.ListResult{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list email templates").
			Err()
	}
	settings, err := u.settings.ListGlobal(ctx)
	if err != nil {
		return emailModel.ListResult{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list notification settings").
			Err()
	}
	emailEnabled := make(map[string]bool)
	for _, s := range settings {
		if s.Channel == string(notificationTypes.NotificationChannelEmail) && s.Enabled {
			emailEnabled[s.NotificationType] = true
		}
	}
	hasPublished := make(map[string]bool)
	for _, t := range templates {
		if t.IsPublished() {
			hasPublished[t.NotificationType] = true
		}
	}
	var missing []string
	for typ := range emailEnabled {
		if !hasPublished[typ] {
			missing = append(missing, typ)
		}
	}
	return emailModel.ListResult{
		Templates:        templates,
		MissingActiveFor: missing,
	}, nil
}

func (u *NotificationEmailTemplateUseCase) PublishEmailTemplate(
	ctx context.Context, id, updatedBy uuid.UUID,
) (emailModel.EmailTemplate, error) {
	template, err := u.platformTemplate(ctx, id)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := validateEmailTemplate(notificationTypes.NotificationType(template.NotificationType), template.Subject, template.Preheader, template.Body, template.Styling); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := u.images.CheckInlineSize(ctx, template.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	tpl, err := u.templates.Publish(ctx, id, updatedBy)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return emailModel.EmailTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to publish email template").
			Err()
	}
	return tpl, nil
}

func validateEmailTemplate(typ notificationTypes.NotificationType, subject, preheader string, body, styling []byte) error {
	if err := notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelEmail, subject, preheader); err != nil {
		return notificationModel.ErrInvalidTemplateVariables.WithError(err).Err()
	}
	if err := notificationTypes.ValidateEmailBodyVariables(typ, body); err != nil {
		return notificationModel.ErrInvalidTemplateVariables.WithError(err).Err()
	}
	return render.ValidateStyling(styling)
}

func (u *NotificationEmailTemplateUseCase) RollbackEmailTemplate(
	ctx context.Context, sourceID, updatedBy uuid.UUID,
) (emailModel.EmailTemplate, error) {
	if _, err := u.platformTemplate(ctx, sourceID); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	tpl, err := u.templates.Rollback(ctx, sourceID, updatedBy)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
		}
		return emailModel.EmailTemplate{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to rollback email template").
			Err()
	}
	if err := u.images.SyncReferences(ctx, tpl.ID, tpl.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return tpl, nil
}

// LatestEmailTemplates returns one TypeVersions entry per notification type,
// folding all versions into Draft / Published / Unpublished slots.
func (u *NotificationEmailTemplateUseCase) LatestEmailTemplates(
	ctx context.Context,
) ([]emailModel.TypeVersions, error) {
	templates, err := u.templates.List(ctx, "", "", nil)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list email templates").
			Err()
	}

	index := make(map[string]*emailModel.TypeVersions)
	for _, kind := range notificationTypes.Types() {
		if notificationTypes.Supports(kind.Type, notificationTypes.NotificationChannelEmail) {
			index[string(kind.Type)] = &emailModel.TypeVersions{NotificationType: string(kind.Type)}
		}
	}
	for _, t := range templates {
		entry, ok := index[t.NotificationType]
		if !ok {
			entry = &emailModel.TypeVersions{NotificationType: t.NotificationType}
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

	out := make([]emailModel.TypeVersions, 0, len(index))
	for _, v := range index {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NotificationType < out[j].NotificationType })
	return out, nil
}
