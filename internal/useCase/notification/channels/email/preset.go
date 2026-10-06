package emailUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

// NotificationEmailPresetUseCase implements CRUD for email block presets.
type NotificationEmailPresetUseCase struct {
	presets *emailTemplateRepo.Repository
	images  *TemplateImages
}

// NewNotificationEmailPresetUseCase constructs the use case. media backs the
// image validation and the preset's image references.
func NewNotificationEmailPresetUseCase(repo emailTemplateRepo.Queries, media TemplateMedia) *NotificationEmailPresetUseCase {
	presets := emailTemplateRepo.New(repo)
	return &NotificationEmailPresetUseCase{presets: presets, images: NewTemplateImages(media, presets)}
}

// ListEmailBlockPresets returns all block presets ordered by name.
func (u *NotificationEmailPresetUseCase) ListEmailBlockPresets(ctx context.Context) ([]emailModel.BlockPreset, error) {
	presets, err := u.presets.ListPresets(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list email block presets").Err()
	}
	return presets, nil
}

// GetEmailBlockPreset returns a single preset by id, or ErrPresetNotFound when absent.
func (u *NotificationEmailPresetUseCase) GetEmailBlockPreset(ctx context.Context, id uuid.UUID) (emailModel.BlockPreset, error) {
	preset, err := u.presets.GetPreset(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return emailModel.BlockPreset{}, notificationModel.ErrPresetNotFound.Err()
		}
		return emailModel.BlockPreset{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get email block preset").Err()
	}
	return preset, nil
}

// CreateEmailBlockPreset persists a new preset.
func (u *NotificationEmailPresetUseCase) CreateEmailBlockPreset(ctx context.Context, in emailModel.PresetInput) (emailModel.BlockPreset, error) {
	if render.ContainsPreset(in.Blocks) {
		return emailModel.BlockPreset{}, notificationModel.ErrPresetNested.Err()
	}
	if err := u.images.ValidateBody(ctx, in.Blocks); err != nil {
		return emailModel.BlockPreset{}, err
	}
	preset, err := u.presets.CreatePreset(ctx, in)
	if err != nil {
		return emailModel.BlockPreset{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create email block preset").Err()
	}
	if err := u.images.SyncPresetReferences(ctx, preset.ID, preset.Blocks); err != nil {
		return emailModel.BlockPreset{}, err
	}
	return preset, nil
}

// UpdateEmailBlockPreset modifies an existing preset, returning ErrPresetNotFound when absent.
func (u *NotificationEmailPresetUseCase) UpdateEmailBlockPreset(ctx context.Context, id uuid.UUID, in emailModel.PresetInput) (emailModel.BlockPreset, error) {
	if render.ContainsPreset(in.Blocks) {
		return emailModel.BlockPreset{}, notificationModel.ErrPresetNested.Err()
	}
	if err := u.images.ValidateBody(ctx, in.Blocks); err != nil {
		return emailModel.BlockPreset{}, err
	}
	preset, err := u.presets.UpdatePreset(ctx, id, in)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return emailModel.BlockPreset{}, notificationModel.ErrPresetNotFound.Err()
		}
		return emailModel.BlockPreset{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update email block preset").Err()
	}
	if err := u.images.SyncPresetReferences(ctx, preset.ID, preset.Blocks); err != nil {
		return emailModel.BlockPreset{}, err
	}
	return preset, nil
}

// DeleteEmailBlockPreset removes a preset by id, returning ErrPresetNotFound when absent.
func (u *NotificationEmailPresetUseCase) DeleteEmailBlockPreset(ctx context.Context, id uuid.UUID) error {
	if err := u.presets.DeletePreset(ctx, id); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return notificationModel.ErrPresetNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete email block preset").Err()
	}
	return u.images.RemovePresetReferences(ctx, id)
}
