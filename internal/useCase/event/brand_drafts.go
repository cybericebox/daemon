package event

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

const brandDraftLifetime = 24 * time.Hour

type BrandAssetChange struct {
	Action string // keep, replace, remove
	FileID uuid.UUID
}

type EventGeneralInput struct {
	Name        string
	Description string
	Preview     BrandAssetChange
}

type EventGeneralView struct {
	Name   string
	Config EventConfigView
}

type EventAppearanceInput struct {
	Brand   string
	Accent  string
	Logo    BrandAssetChange
	Favicon BrandAssetChange
}

type EventAppearanceView struct {
	Config     EventConfigView
	LogoURL    string
	FaviconURL string
}

func brandDraftName(eventID uuid.UUID, kind string) string {
	return fmt.Sprintf("event-brand-draft:%s:%s", eventID, kind)
}

// brandContentTypeAllowed is the image types a brand draft of the kind may be (the upload route's own rule).
func brandContentTypeAllowed(kind, contentType string) bool {
	if kind == "favicon" {
		return contentType == "image/png"
	}
	return contentType == "image/png" || contentType == "image/jpeg" || contentType == "image/webp"
}

func brandKindLimits(kind string) (int64, error) {
	switch kind {
	case "logo":
		return maxEventLogoBytes, nil
	case "preview":
		return maxEventPreviewPictureBytes, nil
	case "favicon":
		return 512 << 10, nil
	default:
		return 0, eventModel.ErrEventBrandDraftInvalid.Err()
	}
}

// UploadEventBrandDraft stores an unreferenced media file. Existing orphan GC
// removes it after the configured grace period if Save never claims it.
func (u *EventUseCase) UploadEventBrandDraft(ctx context.Context, eventID, userID uuid.UUID, kind string, reader io.Reader, size int64) (uuid.UUID, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return uuid.Nil, err
	}
	maxBytes, err := brandKindLimits(kind)
	if err != nil {
		return uuid.Nil, err
	}
	if size > maxBytes {
		return uuid.Nil, eventModel.ErrEventBrandDraftInvalid.Err()
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return uuid.Nil, err
	}
	if int64(len(data)) > maxBytes {
		return uuid.Nil, eventModel.ErrEventBrandDraftInvalid.Err()
	}
	contentType := http.DetectContentType(data)
	if !brandContentTypeAllowed(kind, contentType) {
		return uuid.Nil, eventModel.ErrEventBrandDraftInvalid.Err()
	}
	if mediaModel.IsRasterType(contentType) {
		if err = mediaModel.CheckImagePixels(data); err != nil {
			return uuid.Nil, err
		}
	}
	file, err := u.brandMedia.UploadFile(ctx, brandDraftName(eventID, kind), contentType, bytes.NewReader(data), userID)
	if err != nil {
		return uuid.Nil, err
	}
	return file.ID, nil
}

func (u *EventUseCase) validateBrandChange(ctx context.Context, eventID, userID uuid.UUID, kind string, change BrandAssetChange) ([]uuid.UUID, error) {
	switch change.Action {
	case "keep":
		if change.FileID != uuid.Nil {
			return nil, eventModel.ErrEventBrandDraftInvalid.Err()
		}
		return nil, nil
	case "remove":
		if change.FileID != uuid.Nil {
			return nil, eventModel.ErrEventBrandDraftInvalid.Err()
		}
		return []uuid.UUID{}, nil
	case "replace":
		if change.FileID == uuid.Nil {
			return nil, eventModel.ErrEventBrandDraftInvalid.Err()
		}
		file, err := u.brandMedia.GetFile(ctx, change.FileID)
		if err != nil {
			return nil, err
		}
		if file.Name != brandDraftName(eventID, kind) || !file.CreatedBy.Valid || file.CreatedBy.UUID != userID || time.Since(file.CreatedAt) > brandDraftLifetime || file.CreatedAt.After(time.Now().Add(time.Minute)) {
			return nil, eventModel.ErrEventBrandDraftInvalid.Err()
		}
		// The name and the owner say who made the file, not what it is: another upload route takes
		// a caller-chosen name and any content type. Only an image the draft route itself would
		// have accepted becomes public as a logo, preview or favicon.
		if !brandContentTypeAllowed(kind, file.ContentType) {
			return nil, eventModel.ErrEventBrandDraftInvalid.Err()
		}
		return []uuid.UUID{change.FileID}, nil
	default:
		return nil, eventModel.ErrEventBrandDraftInvalid.Err()
	}
}

func (u *EventUseCase) EventFaviconURL(ctx context.Context, eventID uuid.UUID) (string, error) {
	if u.brandMedia == nil {
		return "", nil
	}
	ids, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventFavicon, eventID)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return fmt.Sprintf("/api/events/%s/favicon/%s", eventID, ids[0]), nil
}

func (u *EventUseCase) StreamEventFavicon(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, "", err
	}
	ids, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventFavicon, eventID)
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 || ids[0] != fileID {
		return nil, "", mediaModel.ErrFileNotFound.Err()
	}
	reader, file, err := u.brandMedia.StreamFile(ctx, fileID)
	if err != nil {
		return nil, "", err
	}
	return reader, file.ContentType, nil
}

// SaveEventGeneral applies the complete General form. An invalid draft never
// changes public state; compensating writes restore the old values if a later
// repository operation fails.
func (u *EventUseCase) SaveEventGeneral(ctx context.Context, eventID, userID uuid.UUID, input EventGeneralInput) (EventGeneralView, error) {
	if strings.TrimSpace(input.Name) == "" {
		return EventGeneralView{}, eventModel.ErrEventNameInvalid.Err()
	}
	newRefs, err := u.validateBrandChange(ctx, eventID, userID, "preview", input.Preview)
	if err != nil {
		return EventGeneralView{}, err
	}
	oldConfig, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return EventGeneralView{}, err
	}
	oldName, err := u.GetEventPublicName(ctx, eventID)
	if err != nil {
		return EventGeneralView{}, err
	}
	config := configInputFromView(oldConfig)
	config.PreviewDescription = input.Description
	oldRefs, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID)
	if err != nil {
		return EventGeneralView{}, err
	}
	if input.Preview.Action == "keep" {
		config.PreviewPicture = oldConfig.PreviewPicture
	}
	if input.Preview.Action == "remove" {
		config.PreviewPicture = ""
	}
	if input.Preview.Action == "replace" {
		config.PreviewPicture, err = u.eventPreviewPictureURL(eventID, input.Preview.FileID)
		if err != nil {
			return EventGeneralView{}, err
		}
	}
	updated, err := u.UpdateEventConfig(ctx, eventID, config, userID)
	if err != nil {
		return EventGeneralView{}, err
	}
	rollbackConfig := func() error {
		_, e := u.UpdateEventConfig(ctx, eventID, configInputFromView(oldConfig), userID)
		return e
	}
	if input.Preview.Action != "keep" {
		if err = u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID, newRefs); err != nil {
			return EventGeneralView{}, errors.Join(err, rollbackConfig())
		}
	}
	name := oldName
	if strings.TrimSpace(input.Name) != oldName {
		name, err = u.UpdateEventPublicName(ctx, eventID, input.Name, userID)
		if err != nil {
			if input.Preview.Action != "keep" {
				err = errors.Join(err, u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID, oldRefs))
			}
			return EventGeneralView{}, errors.Join(err, rollbackConfig())
		}
	}
	return EventGeneralView{Name: name, Config: updated}, nil
}

func configInputFromView(view EventConfigView) UpdateConfigInput {
	return UpdateConfigInput{Participation: view.Participation, Registration: view.Registration, ScoreboardVisibility: view.ScoreboardVisibility, ParticipantsVisibility: view.ParticipantsVisibility, PreviewDescription: view.PreviewDescription, PreviewPicture: view.PreviewPicture, MaxTeamSize: view.MaxTeamSize, MinTeamSize: view.MinTeamSize, MaxTeams: view.MaxTeams}
}

func (u *EventUseCase) SaveEventAppearance(ctx context.Context, eventID, userID uuid.UUID, input EventAppearanceInput) (EventAppearanceView, error) {
	logoRefs, err := u.validateBrandChange(ctx, eventID, userID, "logo", input.Logo)
	if err != nil {
		return EventAppearanceView{}, err
	}
	faviconRefs, err := u.validateBrandChange(ctx, eventID, userID, "favicon", input.Favicon)
	if err != nil {
		return EventAppearanceView{}, err
	}
	oldConfig, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return EventAppearanceView{}, err
	}
	oldLogo, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventLogo, eventID)
	if err != nil {
		return EventAppearanceView{}, err
	}
	oldFavicon, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventFavicon, eventID)
	if err != nil {
		return EventAppearanceView{}, err
	}
	updated := oldConfig
	themeChanged := !strings.EqualFold(strings.TrimSpace(input.Brand), oldConfig.Theme.Brand) || !strings.EqualFold(strings.TrimSpace(input.Accent), oldConfig.Theme.Accent)
	if themeChanged {
		updated, err = u.UpdateEventTheme(ctx, eventID, input.Brand, input.Accent, userID)
		if err != nil {
			return EventAppearanceView{}, err
		}
	}
	rollbackTheme := func() error {
		if !themeChanged {
			return nil
		}
		_, e := u.UpdateEventTheme(ctx, eventID, oldConfig.Theme.Brand, oldConfig.Theme.Accent, userID)
		return e
	}
	if input.Logo.Action != "keep" {
		if err = u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventLogo, eventID, logoRefs); err != nil {
			return EventAppearanceView{}, errors.Join(err, rollbackTheme())
		}
	}
	if input.Favicon.Action != "keep" {
		if err = u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventFavicon, eventID, faviconRefs); err != nil {
			if input.Logo.Action != "keep" {
				err = errors.Join(err, u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventLogo, eventID, oldLogo))
			}
			return EventAppearanceView{}, errors.Join(err, rollbackTheme())
		}
	}
	if input.Logo.Action != "keep" {
		oldLogo = logoRefs
	}
	if input.Favicon.Action != "keep" {
		oldFavicon = faviconRefs
	}
	logoURL, faviconURL := "", ""
	if len(oldLogo) > 0 {
		logoURL = fmt.Sprintf("/api/events/%s/logo/%s", eventID, oldLogo[0])
	}
	if len(oldFavicon) > 0 {
		faviconURL = fmt.Sprintf("/api/events/%s/favicon/%s", eventID, oldFavicon[0])
	}
	return EventAppearanceView{Config: updated, LogoURL: logoURL, FaviconURL: faviconURL}, nil
}
