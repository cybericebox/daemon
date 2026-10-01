package event

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

const maxEventContentImageBytes = 5 << 20

func eventContentImageURL(eventID, fileID uuid.UUID) string {
	return fmt.Sprintf("/api/events/%s/content-images/%s", eventID, fileID)
}

// UploadEventContentImage stores a banner image without modifying the event preview.
func (u *EventUseCase) UploadEventContentImage(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return "", err
	}
	if u.brandMedia == nil {
		return "", mediaModel.ErrMediaStorageNotConfigured.Err()
	}
	if size > maxEventContentImageBytes {
		return "", eventModel.ErrEventPreviewPictureTooLarge.Err()
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxEventContentImageBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxEventContentImageBytes {
		return "", eventModel.ErrEventPreviewPictureTooLarge.Err()
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" && contentType != "image/gif" {
		return "", eventModel.ErrEventPreviewPictureTypeInvalid.Err()
	}
	file, err := u.brandMedia.UploadFile(ctx, "event-content-image", contentType, bytes.NewReader(data), userID)
	if err != nil {
		return "", err
	}
	if err := u.brandMedia.AddReference(ctx, mediaModel.RefTypeEventContentImage, eventID, file.ID); err != nil {
		return "", err
	}
	return eventContentImageURL(eventID, file.ID), nil
}

func (u *EventUseCase) StreamEventContentImage(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, "", err
	}
	if u.brandMedia == nil {
		return nil, "", mediaModel.ErrMediaStorageNotConfigured.Err()
	}
	refs, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventContentImage, eventID)
	if err != nil {
		return nil, "", err
	}
	found := false
	for _, ref := range refs {
		if ref == fileID {
			found = true
			break
		}
	}
	if !found {
		return nil, "", mediaModel.ErrFileNotFound.Err()
	}
	reader, file, err := u.brandMedia.StreamFile(ctx, fileID)
	if err != nil {
		return nil, "", err
	}
	return reader, file.ContentType, nil
}

func (u *EventUseCase) validateContentImageURLs(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document) error {
	var refs []uuid.UUID
	// Custom banner images and partner logos must be this event's uploads.
	for _, imageURL := range document.ContentImageURLs() {
		const prefix = "/api/events/"
		if !strings.HasPrefix(imageURL, prefix) {
			return eventContentModel.ErrPageInvalid.Err()
		}
		fileID, err := uuid.FromString(strings.TrimPrefix(imageURL, prefix+eventID.String()+"/content-images/"))
		if err != nil || imageURL != eventContentImageURL(eventID, fileID) {
			return eventContentModel.ErrPageInvalid.Err()
		}
		refs = append(refs, fileID)
	}
	if len(refs) == 0 {
		return nil
	}
	if u.brandMedia == nil {
		return eventContentModel.ErrPageInvalid.Err()
	}
	owned, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventContentImage, eventID)
	if err != nil {
		return err
	}
	for _, fileID := range refs {
		found := false
		for _, ref := range owned {
			if ref == fileID {
				found = true
				break
			}
		}
		if !found {
			return eventContentModel.ErrPageInvalid.Err()
		}
	}
	return nil
}
