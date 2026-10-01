package emailUseCase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// fakeTemplateMedia is an in-memory TemplateMedia recording reference calls.
type fakeTemplateMedia struct {
	files    map[uuid.UUID]mediaModel.File
	blobs    map[uuid.UUID][]byte
	replaced map[uuid.UUID][]uuid.UUID
	removed  []uuid.UUID
	refTypes []string
}

func newFakeTemplateMedia() *fakeTemplateMedia {
	return &fakeTemplateMedia{
		files:    map[uuid.UUID]mediaModel.File{},
		blobs:    map[uuid.UUID][]byte{},
		replaced: map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeTemplateMedia) addFile(contentType string, size int64) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.files[id] = mediaModel.File{ID: id, ContentType: contentType, SizeBytes: size}
	return id
}

func (f *fakeTemplateMedia) UploadFile(_ context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return mediaModel.File{}, err
	}
	id := uuid.Must(uuid.NewV7())
	file := mediaModel.File{ID: id, Name: name, ContentType: contentType, SizeBytes: int64(len(b)), CreatedBy: uuid.NullUUID{UUID: createdBy, Valid: true}}
	f.files[id] = file
	f.blobs[id] = b
	return file, nil
}

func (f *fakeTemplateMedia) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	file, ok := f.files[id]
	if !ok {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return io.NopCloser(bytes.NewReader(f.blobs[id])), file, nil
}

func (f *fakeTemplateMedia) GetFile(_ context.Context, id uuid.UUID) (mediaModel.File, error) {
	file, ok := f.files[id]
	if !ok {
		return mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return file, nil
}

func (f *fakeTemplateMedia) ReplaceReferences(_ context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error {
	f.refTypes = append(f.refTypes, refType)
	f.replaced[refID] = fileIDs
	return nil
}

func (f *fakeTemplateMedia) AddReference(_ context.Context, refType string, refID, fileID uuid.UUID) error {
	f.refTypes = append(f.refTypes, refType)
	f.replaced[refID] = append(f.replaced[refID], fileID)
	return nil
}

func (f *fakeTemplateMedia) RemoveReferences(_ context.Context, refType string, refID uuid.UUID) error {
	f.refTypes = append(f.refTypes, refType)
	f.removed = append(f.removed, refID)
	return nil
}

func imageBody(ids ...string) json.RawMessage {
	var blocks []map[string]any
	for _, id := range ids {
		blocks = append(blocks, map[string]any{"type": "image", "file_id": id})
	}
	b, _ := json.Marshal(blocks)
	return b
}

func TestTemplateImages_ValidateBody(t *testing.T) {
	m := newFakeTemplateMedia()
	ok := m.addFile("image/png", 10)
	imgs := emailUseCase.NewTemplateImages(m, fakePresets{})
	ctx := context.Background()

	require.NoError(t, imgs.ValidateBody(ctx, imageBody(ok.String())))
	require.NoError(t, imgs.ValidateBody(ctx, json.RawMessage(`[{"type":"image","url":"https://x/y.png"}]`)))

	err := imgs.ValidateBody(ctx, imageBody("not-a-uuid"))
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "unparsable file_id: %v", err)

	err = imgs.ValidateBody(ctx, imageBody(uuid.Must(uuid.NewV7()).String()))
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "unknown file: %v", err)
}

func TestTemplateImages_CheckInlineSize(t *testing.T) {
	m := newFakeTemplateMedia()
	imgs := emailUseCase.NewTemplateImages(m, fakePresets{})
	ctx := context.Background()
	logo := int64(len(branding.LogoPNG()))

	// Exactly at the cap together with the logo → allowed.
	atCap := m.addFile("image/png", emailUseCase.MaxInlineBytes-logo)
	body := json.RawMessage(`[{"type":"logo"},{"type":"image","file_id":"` + atCap.String() + `"}]`)
	require.NoError(t, imgs.CheckInlineSize(ctx, body))

	// One byte more → rejected.
	over := m.addFile("image/png", emailUseCase.MaxInlineBytes-logo+1)
	body = json.RawMessage(`[{"type":"logo"},{"type":"image","file_id":"` + over.String() + `"}]`)
	err := imgs.CheckInlineSize(ctx, body)
	assert.True(t, notificationModel.ErrTemplateInlineTooLarge.Err().Is(err), "%v", err)

	// Without the logo block the logo does not count.
	require.NoError(t, imgs.CheckInlineSize(ctx, json.RawMessage(`[{"type":"image","file_id":"`+atCap.String()+`"}]`)))

	// A file referenced twice is attached (and counted) once.
	half := m.addFile("image/png", emailUseCase.MaxInlineBytes/2+1)
	require.NoError(t, imgs.CheckInlineSize(ctx, imageBody(half.String(), half.String())))
}

func TestTemplateImages_CheckInlineSize_MissingFileIsInvalid(t *testing.T) {
	imgs := emailUseCase.NewTemplateImages(newFakeTemplateMedia(), fakePresets{})
	err := imgs.CheckInlineSize(context.Background(), imageBody(uuid.Must(uuid.NewV7()).String()))
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}

func TestTemplateImages_SyncAndRemoveReferences(t *testing.T) {
	m := newFakeTemplateMedia()
	imgs := emailUseCase.NewTemplateImages(m, fakePresets{})
	ctx := context.Background()
	tplID := uuid.Must(uuid.NewV7())
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	require.NoError(t, imgs.SyncReferences(ctx, tplID, imageBody(a.String(), b.String(), a.String())))
	assert.Equal(t, []uuid.UUID{a, b}, m.replaced[tplID])

	require.NoError(t, imgs.RemoveReferences(ctx, tplID))
	assert.Equal(t, []uuid.UUID{tplID}, m.removed)
	for _, rt := range m.refTypes {
		assert.Equal(t, mediaModel.RefTypeEmailTemplate, rt)
	}
}

func TestUploadEmailImage_StoresProcessedImage(t *testing.T) {
	m := newFakeTemplateMedia()
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(nil, m)
	userID := uuid.Must(uuid.NewV7())
	src := image.NewRGBA(image.Rect(0, 0, 1600, 400))
	for i := range src.Pix {
		src.Pix[i] = 0xFF
	}
	src.Set(0, 0, color.RGBA{A: 0xFF})

	f, err := uc.UploadEmailImage(context.Background(), bytes.NewReader(encodePNG(t, src)), "hero.png", userID)
	require.NoError(t, err)
	assert.Equal(t, "image/png", f.ContentType)
	assert.Equal(t, "hero.png", f.Name)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(m.blobs[f.ID]))
	require.NoError(t, err)
	assert.Equal(t, 1200, cfg.Width)
}

func TestUploadEmailImage_RejectsSVGBeforeStoring(t *testing.T) {
	m := newFakeTemplateMedia()
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(nil, m)

	_, err := uc.UploadEmailImage(context.Background(), bytes.NewReader([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)), "x.svg", uuid.Nil)
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err))
	assert.Empty(t, m.files)
}

func TestStreamEmailImage_OnlyServesTemplateImageTypes(t *testing.T) {
	m := newFakeTemplateMedia()
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(nil, m)
	ctx := context.Background()
	img := m.addFile("image/jpeg", 3)
	other := m.addFile("application/pdf", 3)

	rc, f, err := uc.StreamEmailImage(ctx, img)
	require.NoError(t, err)
	_ = rc.Close()
	assert.Equal(t, img, f.ID)

	_, _, err = uc.StreamEmailImage(ctx, other)
	assert.True(t, mediaModel.ErrFileNotFound.Err().Is(err))
}

// fakeEventImages is an in-memory EventImageSource: usable[event] lists the
// files that Event's templates may use.
type fakeEventImages struct {
	usable map[uuid.UUID][]uuid.UUID
	asked  []uuid.UUID
}

func (f *fakeEventImages) FileUsableByEvent(_ context.Context, fileID, eventID uuid.UUID) (bool, error) {
	f.asked = append(f.asked, fileID)
	for _, id := range f.usable[eventID] {
		if id == fileID {
			return true, nil
		}
	}
	return false, nil
}

func TestTemplateImages_ValidateEventBody(t *testing.T) {
	m := newFakeTemplateMedia()
	usable := m.addFile("image/png", 3)
	foreign := m.addFile("image/png", 3) // e.g. another user's avatar
	kept := m.addFile("image/png", 3)    // already in the row being updated
	eventID := uuid.Must(uuid.NewV7())
	src := &fakeEventImages{usable: map[uuid.UUID][]uuid.UUID{eventID: {usable}}}
	imgs := emailUseCase.NewTemplateImages(m, fakePresets{})
	ctx := context.Background()

	require.NoError(t, imgs.ValidateEventBody(ctx, src, eventID, imageBody(usable.String()), nil))

	err := imgs.ValidateEventBody(ctx, src, eventID, imageBody(foreign.String()), nil)
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "foreign file: %v", err)

	// Another Event's allow-list does not count.
	err = imgs.ValidateEventBody(ctx, src, uuid.Must(uuid.NewV7()), imageBody(usable.String()), nil)
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "other event: %v", err)

	// Files the row already has are not re-checked; new ones are.
	src.asked = nil
	require.NoError(t, imgs.ValidateEventBody(ctx, src, eventID, imageBody(kept.String(), usable.String()), imageBody(kept.String())))
	assert.Equal(t, []uuid.UUID{usable}, src.asked)

	// The existence/type checks of ValidateBody still apply first.
	src.asked = nil
	err = imgs.ValidateEventBody(ctx, src, eventID, imageBody(uuid.Must(uuid.NewV7()).String()), nil)
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "unknown file: %v", err)
	assert.Empty(t, src.asked)
}

func TestTemplateImages_StreamForEvent(t *testing.T) {
	m := newFakeTemplateMedia()
	add := func(contentType string) uuid.UUID {
		id := m.addFile(contentType, 3)
		m.blobs[id] = []byte("img")
		return id
	}
	png, jpeg, notImage, foreign := add("image/png"), add("image/jpeg"), add("application/pdf"), add("image/png")
	eventID := uuid.Must(uuid.NewV7())
	src := &fakeEventImages{usable: map[uuid.UUID][]uuid.UUID{eventID: {png, jpeg, notImage}}}
	imgs := emailUseCase.NewTemplateImages(m, fakePresets{})
	ctx := context.Background()

	for _, id := range []uuid.UUID{png, jpeg} {
		rc, f, err := imgs.StreamForEvent(ctx, src, eventID, id)
		require.NoError(t, err)
		require.Equal(t, id, f.ID)
		b, _ := io.ReadAll(rc)
		require.Equal(t, "img", string(b))
		require.NoError(t, rc.Close())
	}
	for _, id := range []uuid.UUID{foreign, notImage, uuid.Must(uuid.NewV7())} {
		_, _, err := imgs.StreamForEvent(ctx, src, eventID, id)
		assert.True(t, mediaModel.ErrFileNotFound.Err().Is(err), "file %s: %v", id, err)
	}
	// A file usable by this Event is not served under another Event.
	_, _, err := imgs.StreamForEvent(ctx, src, uuid.Must(uuid.NewV7()), png)
	assert.True(t, mediaModel.ErrFileNotFound.Err().Is(err), "other event: %v", err)
}
