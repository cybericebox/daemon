package event

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"testing"

	contentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type renderImageMedia struct {
	BrandMedia
	refs  map[string][]uuid.UUID
	data  []byte
	reads int
}

func (m *renderImageMedia) GetReferences(_ context.Context, kind string, _ uuid.UUID) ([]uuid.UUID, error) {
	return m.refs[kind], nil
}
func (m *renderImageMedia) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	m.reads++
	return io.NopCloser(bytes.NewReader(m.data)), mediaModel.File{ID: id, ContentType: "image/png"}, nil
}
func TestRenderBannerDimensionsUseOnlyOwnedImagesAndReadDuplicatesOnce(t *testing.T) {
	eventID, fileID, otherID := uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4())
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 128, 64))))
	media := &renderImageMedia{refs: map[string][]uuid.UUID{mediaModel.RefTypeEventContentImage: {fileID}}, data: data.Bytes()}
	uc := &EventUseCase{brandMedia: media}
	own, foreign := eventContentImageURL(eventID, fileID), eventContentImageURL(eventID, otherID)
	document := contentModel.Document{Blocks: []contentModel.Block{{ID: "one", Type: contentModel.BlockBanner, ImageSource: "custom", ImageURL: own}, {ID: "two", Type: contentModel.BlockBanner, ImageSource: "custom", ImageURL: own}, {ID: "foreign", Type: contentModel.BlockBanner, ImageSource: "custom", ImageURL: foreign}}}
	dims := uc.renderBannerDimensions(context.Background(), eventID, document, "")
	require.Equal(t, map[string]RenderImageDimensions{own: {Width: 128, Height: 64}}, dims)
	require.Equal(t, 1, media.reads)
}
func TestRenderBannerDimensionsGracefullySkipInvalidHeaders(t *testing.T) {
	eventID, fileID := uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4())
	media := &renderImageMedia{refs: map[string][]uuid.UUID{mediaModel.RefTypeEventContentImage: {fileID}}, data: []byte("invalid")}
	uc := &EventUseCase{brandMedia: media}
	require.Empty(t, uc.renderBannerDimensions(context.Background(), eventID, contentModel.Document{Blocks: []contentModel.Block{{Type: contentModel.BlockBanner, ImageSource: "custom", ImageURL: eventContentImageURL(eventID, fileID)}}}, ""))
}
