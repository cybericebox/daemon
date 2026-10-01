package event

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/gofrs/uuid"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

const maxLiveLogoBytes = 1 << 20

// liveLogoType sniffs the content (never the file name): PNG and WebP as
// they are, SVG only after the allowlist rebuild (mediaModel.SanitizeSVG).
func liveLogoType(data []byte) (string, []byte, error) {
	switch http.DetectContentType(data) {
	case "image/png":
		return "image/png", data, nil
	case "image/webp":
		return "image/webp", data, nil
	}
	if mediaModel.LooksLikeSVG(data) {
		clean, err := mediaModel.SanitizeSVG(data)
		if err == nil {
			return "image/svg+xml", clean, nil
		}
	}
	return "", nil, eventContentModel.ErrLiveLogoTypeInvalid.Err()
}

// UploadLiveLogo stores an organizer or partner logo for the Live logos
// widgets and returns its site path (served like content images, with a
// sandboxing CSP).
func (u *EventUseCase) UploadLiveLogo(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return "", err
	}
	if u.brandMedia == nil {
		return "", mediaModel.ErrMediaStorageNotConfigured.Err()
	}
	if size > maxLiveLogoBytes {
		return "", eventContentModel.ErrLiveLogoTooLarge.Err()
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxLiveLogoBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxLiveLogoBytes {
		return "", eventContentModel.ErrLiveLogoTooLarge.Err()
	}
	contentType, clean, err := liveLogoType(data)
	if err != nil {
		return "", err
	}
	file, err := u.brandMedia.UploadFile(ctx, "event-live-logo", contentType, bytes.NewReader(clean), userID)
	if err != nil {
		return "", err
	}
	if err := u.brandMedia.AddReference(ctx, mediaModel.RefTypeEventContentImage, eventID, file.ID); err != nil {
		return "", err
	}
	return eventContentImageURL(eventID, file.ID), nil
}
