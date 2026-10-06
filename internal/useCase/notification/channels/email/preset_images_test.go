package emailUseCase_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/tools"
)

// fakePresets is an in-memory preset source for TemplateImages.
type fakePresets map[uuid.UUID]json.RawMessage

func (f fakePresets) GetPreset(_ context.Context, id uuid.UUID) (emailModel.BlockPreset, error) {
	blocks, ok := f[id]
	if !ok {
		return emailModel.BlockPreset{}, pgx.ErrNoRows
	}
	return emailModel.BlockPreset{ID: id, Blocks: blocks}, nil
}

func presetBlock(id uuid.UUID) string {
	return `{"type":"preset","preset_id":"` + id.String() + `"}`
}

func TestTemplateImages_CheckInlineSize_CountsPresetImages(t *testing.T) {
	m := newFakeTemplateMedia()
	half := m.addFile("image/png", emailUseCase.MaxInlineBytes/2+1)
	other := m.addFile("image/png", emailUseCase.MaxInlineBytes/2)
	presetID := uuid.Must(uuid.NewV7())
	presets := fakePresets{presetID: imageBody(other.String())}
	imgs := emailUseCase.NewTemplateImages(m, presets)

	body := json.RawMessage(`[{"type":"image","file_id":"` + half.String() + `"},` + presetBlock(presetID) + `]`)
	err := imgs.CheckInlineSize(context.Background(), body)
	assert.True(t, notificationModel.ErrTemplateInlineTooLarge.Err().Is(err), "%v", err)

	// The same file in body and preset is attached once.
	presets[presetID] = imageBody(half.String())
	require.NoError(t, imgs.CheckInlineSize(context.Background(), body))
}

func TestTemplateImages_CheckInlineSize_CountsLogoInPresetAndNestedPresets(t *testing.T) {
	m := newFakeTemplateMedia()
	logo := int64(len(branding.LogoPNG()))
	file := m.addFile("image/png", emailUseCase.MaxInlineBytes-logo+1)
	outer, inner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	presets := fakePresets{
		outer: json.RawMessage(`[` + presetBlock(inner) + `,` + presetBlock(outer) + `]`), // self-cycle is tolerated
		inner: json.RawMessage(`[{"type":"logo"}]`),
	}
	imgs := emailUseCase.NewTemplateImages(m, presets)

	body := json.RawMessage(`[{"type":"image","file_id":"` + file.String() + `"},` + presetBlock(outer) + `]`)
	err := imgs.CheckInlineSize(context.Background(), body)
	assert.True(t, notificationModel.ErrTemplateInlineTooLarge.Err().Is(err), "%v", err)
}

func TestTemplateImages_CheckInlineSize_IgnoresMissingPreset(t *testing.T) {
	imgs := emailUseCase.NewTemplateImages(newFakeTemplateMedia(), fakePresets{})
	body := json.RawMessage(`[` + presetBlock(uuid.Must(uuid.NewV7())) + `,{"type":"preset","preset_id":"junk"}]`)
	require.NoError(t, imgs.CheckInlineSize(context.Background(), body))
}

func TestTemplateImages_ValidateBody_RejectsNonImageFile(t *testing.T) {
	m := newFakeTemplateMedia()
	pdf := m.addFile("application/pdf", 10)
	gifFile := m.addFile("image/gif", 10)
	imgs := emailUseCase.NewTemplateImages(m, fakePresets{})

	for _, id := range []uuid.UUID{pdf, gifFile} {
		err := imgs.ValidateBody(context.Background(), imageBody(id.String()))
		assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
	}
}

func TestPresetCreate_ValidatesAndReferencesImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	file := m.addFile("image/png", 10)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, m)
	blocks := imageBody(file.String())

	repo.EXPECT().CreateEmailBlockPreset(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEmailBlockPresetParams) (postgres.NotificationEmailBlockPreset, error) {
			return postgres.NotificationEmailBlockPreset{ID: arg.ID, Name: arg.Name, Blocks: arg.Blocks}, nil
		})

	got, err := uc.CreateEmailBlockPreset(context.Background(), emailModel.PresetInput{Name: "hero", Blocks: blocks})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{file}, m.replaced[got.ID])
	assert.Equal(t, []string{mediaModel.RefTypeEmailBlockPreset}, m.refTypes)
}

func TestPresetCreate_RejectsInvalidImage(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl) // CreateEmailBlockPreset must NOT be called
	m := newFakeTemplateMedia()
	pdf := m.addFile("application/pdf", 10)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, m)

	for _, id := range []string{"nope", uuid.Must(uuid.NewV7()).String(), pdf.String()} {
		_, err := uc.CreateEmailBlockPreset(context.Background(), emailModel.PresetInput{Name: "x", Blocks: imageBody(id)})
		assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%s: %v", id, err)
	}
}

func TestPresetUpdate_ReplacesImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	file := m.addFile("image/jpeg", 10)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, m)
	id := tools.NewUUIDv7()
	blocks := imageBody(file.String())

	repo.EXPECT().UpdateEmailBlockPreset(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationEmailBlockPreset{ID: id, Name: "x", Blocks: blocks}, nil)

	_, err := uc.UpdateEmailBlockPreset(context.Background(), id, emailModel.PresetInput{Name: "x", Blocks: blocks})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{file}, m.replaced[id])
	assert.Equal(t, []string{mediaModel.RefTypeEmailBlockPreset}, m.refTypes)
}

func TestPresetUpdate_RejectsInvalidImage(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl) // UpdateEmailBlockPreset must NOT be called
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	_, err := uc.UpdateEmailBlockPreset(context.Background(), tools.NewUUIDv7(),
		emailModel.PresetInput{Name: "x", Blocks: imageBody(uuid.Must(uuid.NewV7()).String())})
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}

func TestPresetDelete_RemovesImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, m)
	id := tools.NewUUIDv7()
	repo.EXPECT().DeleteEmailBlockPreset(gomock.Any(), id).Return(nil)

	require.NoError(t, uc.DeleteEmailBlockPreset(context.Background(), id))
	assert.Equal(t, []uuid.UUID{id}, m.removed)
	assert.Equal(t, []string{mediaModel.RefTypeEmailBlockPreset}, m.refTypes)
}

func TestPublishEmailTemplate_CountsPresetImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl) // PublishEmailTemplate must NOT be called
	m := newFakeTemplateMedia()
	big := m.addFile("image/png", emailUseCase.MaxInlineBytes+1)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	id := tools.NewUUIDv7()
	presetID := tools.NewUUIDv7()

	row := platformEmailRow(id)
	row.Body = []byte(`[` + presetBlock(presetID) + `]`)
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(row, nil)
	repo.EXPECT().GetEmailBlockPreset(gomock.Any(), presetID).
		Return(postgres.NotificationEmailBlockPreset{ID: presetID, Blocks: imageBody(big.String())}, nil)

	_, err := uc.PublishEmailTemplate(context.Background(), id, tools.NewUUIDv7())
	assert.True(t, notificationModel.ErrTemplateInlineTooLarge.Err().Is(err), "%v", err)
}

// hugeDimensionPNG is a PNG whose IHDR claims w×h pixels but carries no image
// data: a few dozen bytes that DecodeConfig accepts.
func hugeDimensionPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 2 // 8-bit truecolor
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestProcessTemplateImage_RejectsHugeDimensionsBeforeDecoding(t *testing.T) {
	in := hugeDimensionPNG(6000, 5000) // 30M pixels > 24M guard
	require.Less(t, len(in), 100)

	_, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(in))
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateImageTooLarge.Err().Is(err), "%v", err)
}

func TestProcessTemplateImage_PixelGuardAllowsBelowLimit(t *testing.T) {
	// 4000×5000 = 20M pixels passes the guard and then fails decoding (no
	// image data) — proving the guard, not the decoder, rejected 30M.
	_, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(hugeDimensionPNG(4000, 5000)))
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}
