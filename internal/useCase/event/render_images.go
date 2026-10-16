package event

import (
	"context"
	"image"
	"io"
	"net/url"
	"strings"

	contentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/gofrs/uuid"
)

// RenderImageDimensions reserves the natural image ratio before browser download.
type RenderImageDimensions struct {
	Width  int `json:"Width"`
	Height int `json:"Height"`
}

// Called only after common-page admission. Only this event's owned media is read;
// a document URL is never fetched. DecodeConfig reads headers, never pixel data.
// Unavailable legacy media does not make the whole page unavailable.
func (u *EventUseCase) renderBannerDimensions(ctx context.Context, eventID uuid.UUID, document contentModel.Document, cover string) map[string]RenderImageDimensions {
	dimensions := make(map[string]RenderImageDimensions)
	if u.brandMedia == nil {
		return dimensions
	}
	references := make(map[string][]uuid.UUID)
	for _, block := range document.Blocks {
		if block.Type != contentModel.BlockBanner {
			continue
		}
		source := cover
		kind := mediaModel.RefTypeEventPreviewPicture
		if block.ImageSource == "custom" {
			source = block.ImageURL
			kind = mediaModel.RefTypeEventContentImage
		}
		if source == "" {
			continue
		}
		if _, exists := dimensions[source]; exists {
			continue
		}
		parsed, err := url.Parse(source)
		if err != nil {
			continue
		}
		segment := "preview-picture"
		if kind == mediaModel.RefTypeEventContentImage {
			segment = "content-images"
		}
		prefix := "/api/events/" + eventID.String() + "/" + segment + "/"
		if !strings.HasPrefix(parsed.Path, prefix) {
			continue
		}
		fileID, err := uuid.FromString(strings.TrimPrefix(parsed.Path, prefix))
		if err != nil || parsed.Path != prefix+fileID.String() {
			continue
		}
		refs, loaded := references[kind]
		if !loaded {
			refs, err = u.brandMedia.GetReferences(ctx, kind, eventID)
			if err != nil {
				continue
			}
			references[kind] = refs
		}
		owned := false
		for _, id := range refs {
			if id == fileID {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		reader, _, err := u.brandMedia.StreamFile(ctx, fileID)
		if err != nil {
			continue
		}
		config, _, decodeErr := image.DecodeConfig(io.LimitReader(reader, 1<<20))
		_ = reader.Close()
		if decodeErr == nil && config.Width > 0 && config.Height > 0 && int64(config.Width)*int64(config.Height) <= mediaModel.MaxImagePixels {
			dimensions[source] = RenderImageDimensions{Width: config.Width, Height: config.Height}
		}
	}
	return dimensions
}
