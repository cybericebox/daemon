package event

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// UploadLimits are the byte limits of the event image uploads (EVENT_LOGO_MAX_BYTES,
// EVENT_PREVIEW_PICTURE_MAX_BYTES, EVENT_CONTENT_IMAGE_MAX_BYTES, LIVE_LOGO_MAX_BYTES).
type UploadLimits struct {
	Logo, PreviewPicture, ContentImage, LiveLogo int64
}

var (
	maxEventLogoBytes           int64 = 2 << 20
	maxEventPreviewPictureBytes int64 = 5 << 20
	maxEventContentImageBytes   int64 = 5 << 20
	maxLiveLogoBytes            int64 = 1 << 20
)

// ConfigureUploadLimits sets the event image upload limits once at start.
func ConfigureUploadLimits(l UploadLimits) {
	maxEventLogoBytes, maxEventPreviewPictureBytes = l.Logo, l.PreviewPicture
	maxEventContentImageBytes, maxLiveLogoBytes = l.ContentImage, l.LiveLogo
}

// EventLogoURL exposes only the current logo's event-scoped proxy URL.
// An absent reference intentionally means the frontend uses the platform logo.
func (u *EventUseCase) EventLogoURL(ctx context.Context, eventID uuid.UUID) (string, error) {
	if u.brandMedia == nil {
		return "", nil
	}
	ids, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventLogo, eventID)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return fmt.Sprintf("/api/events/%s/logo/%s", eventID, ids[0]), nil
}

func (u *EventUseCase) UploadEventLogo(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return "", err
	}
	if size > maxEventLogoBytes {
		return "", eventModel.ErrEventLogoTooLarge.Err()
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxEventLogoBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxEventLogoBytes {
		return "", eventModel.ErrEventLogoTooLarge.Err()
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" {
		return "", eventModel.ErrEventLogoTypeInvalid.Err()
	}
	if err = mediaModel.CheckImagePixels(data); err != nil {
		return "", err
	}
	file, err := u.brandMedia.UploadFile(ctx, "event-logo", contentType, bytes.NewReader(data), userID)
	if err != nil {
		return "", err
	}
	if err := u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventLogo, eventID, []uuid.UUID{file.ID}); err != nil {
		return "", err
	}
	return fmt.Sprintf("/api/events/%s/logo/%s", eventID, file.ID), nil
}

func (u *EventUseCase) RemoveEventLogo(ctx context.Context, eventID uuid.UUID) error {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return err
	}
	return u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventLogo, eventID, nil)
}

func (u *EventUseCase) eventPreviewPictureURL(eventID, fileID uuid.UUID) (string, error) {
	base, err := url.Parse(strings.TrimRight(u.publicAPIBaseURL, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return "", eventConfigModel.ErrPreviewPictureInvalid.Err()
	}
	return fmt.Sprintf("%s/api/events/%s/preview-picture/%s", base.String(), eventID, fileID), nil
}

// UploadEventPreviewPicture changes only the preview image and its event media
// reference. If config persistence fails, the previous reference is restored.
func (u *EventUseCase) UploadEventPreviewPicture(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return "", err
	}
	if _, err := u.eventPreviewPictureURL(eventID, uuid.Nil); err != nil {
		return "", err
	}
	if size > maxEventPreviewPictureBytes {
		return "", eventModel.ErrEventPreviewPictureTooLarge.Err()
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxEventPreviewPictureBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxEventPreviewPictureBytes {
		return "", eventModel.ErrEventPreviewPictureTooLarge.Err()
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" {
		return "", eventModel.ErrEventPreviewPictureTypeInvalid.Err()
	}
	if err = mediaModel.CheckImagePixels(data); err != nil {
		return "", err
	}
	file, err := u.brandMedia.UploadFile(ctx, "event-preview-picture", contentType, bytes.NewReader(data), userID)
	if err != nil {
		return "", err
	}
	pictureURL, err := u.eventPreviewPictureURL(eventID, file.ID)
	if err != nil {
		return "", err
	}
	previous, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID)
	if err != nil {
		return "", err
	}
	if err := u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID, []uuid.UUID{file.ID}); err != nil {
		return "", err
	}
	_, err = u.mutateEventConfig(ctx, eventID, func(cfg *eventConfigModel.EventConfig) error {
		return cfg.SetPreviewPicture(pictureURL, time.Now().UTC(), userID)
	})
	if err != nil {
		return "", errors.Join(err, u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID, previous))
	}
	return pictureURL, nil
}

func (u *EventUseCase) RemoveEventPreviewPicture(ctx context.Context, eventID, userID uuid.UUID) error {
	config, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return err
	}
	_, err = u.mutateEventConfig(ctx, eventID, func(cfg *eventConfigModel.EventConfig) error {
		return cfg.SetPreviewPicture("", time.Now().UTC(), userID)
	})
	if err != nil {
		return err
	}
	if err = u.brandMedia.ReplaceReferences(ctx, mediaModel.RefTypeEventPreviewPicture, eventID, nil); err != nil {
		_, rollbackErr := u.mutateEventConfig(ctx, eventID, func(cfg *eventConfigModel.EventConfig) error {
			return cfg.SetPreviewPicture(config.PreviewPicture, time.Now().UTC(), userID)
		})
		return errors.Join(err, rollbackErr)
	}
	return nil
}

// requireMediaVisible is the gate of the public media routes (logo, favicon, preview picture, page
// images): they are public once the event is published. Before that the event does not exist for
// anyone but its staff, whose own pages show the images while they are being set up. public tells which
// of the two let the caller in.
func (u *EventUseCase) requireMediaVisible(ctx context.Context, eventID uuid.UUID) (public bool, err error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return false, eventModel.ErrEventNotFound.Err()
		}
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	phase := e.Lifecycle.Status(time.Now())
	if phase != eventModel.LifecycleNotPublished && phase != eventModel.LifecycleWithdrawn {
		return true, nil
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return false, eventModel.ErrEventNotFound.Err()
	}
	if err = u.RequireReadEvent(ctx, eventID, claims.UserID); err != nil {
		return false, eventModel.ErrEventNotFound.WithError(err).Err()
	}
	return false, nil
}

// EventMediaKind names a public event image; the file id in its URL is its version.
type EventMediaKind string

const (
	EventMediaLogo           EventMediaKind = "logo"
	EventMediaFavicon        EventMediaKind = "favicon"
	EventMediaPreviewPicture EventMediaKind = "preview-picture"
	EventMediaContentImage   EventMediaKind = "content-images"
)

func (k EventMediaKind) refType() (string, bool) {
	switch k {
	case EventMediaLogo:
		return mediaModel.RefTypeEventLogo, true
	case EventMediaFavicon:
		return mediaModel.RefTypeEventFavicon, true
	case EventMediaPreviewPicture:
		return mediaModel.RefTypeEventPreviewPicture, true
	case EventMediaContentImage:
		return mediaModel.RefTypeEventContentImage, true
	}
	return "", false
}

// CheckEventMedia is the access half of serving an event image, answered from the database alone: the
// event is visible to the caller and the file belongs to it. public is true when the event is published,
// so the file's URL (its id is its version) may be cached for good; false when only staff may see it.
// The bytes are opened afterwards by OpenEventMedia, so a conditional request never touches storage.
func (u *EventUseCase) CheckEventMedia(ctx context.Context, kind EventMediaKind, eventID, fileID uuid.UUID) (bool, error) {
	refType, ok := kind.refType()
	if !ok {
		return false, mediaModel.ErrFileNotFound.Err()
	}
	public, err := u.requireMediaVisible(ctx, eventID)
	if err != nil {
		return false, err
	}
	if u.brandMedia == nil {
		return false, mediaModel.ErrMediaStorageNotConfigured.Err()
	}
	refs, err := u.brandMedia.GetReferences(ctx, refType, eventID)
	if err != nil {
		return false, err
	}
	if kind != EventMediaContentImage && len(refs) > 1 {
		refs = refs[:1] // one current file; older ones are not served
	}
	for _, ref := range refs {
		if ref == fileID {
			return public, nil
		}
	}
	return false, mediaModel.ErrFileNotFound.Err()
}

// OpenEventMedia opens the bytes of a file CheckEventMedia let through.
func (u *EventUseCase) OpenEventMedia(ctx context.Context, fileID uuid.UUID) (io.ReadCloser, string, error) {
	reader, file, err := u.brandMedia.StreamFile(ctx, fileID)
	if err != nil {
		return nil, "", err
	}
	return reader, file.ContentType, nil
}
