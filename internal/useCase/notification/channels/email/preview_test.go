package emailUseCase_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

const previewType = notificationTypes.NotificationType(signalModel.TypeParticipantOpenRegistrationCompleted)

// dataURI is the data: URI the preview inlines an image as.
func dataURI(contentType string, data []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// addBlob stores an uploaded file with its bytes in the fake media.
func (f *fakeTemplateMedia) addBlob(contentType string, data []byte) uuid.UUID {
	id := f.addFile(contentType, int64(len(data)))
	f.blobs[id] = data
	return id
}

// requireInvalidInput asserts err is target and a 4xx.
func requireInvalidInput(t *testing.T, target appErr.ErrorCreator, err error) {
	t.Helper()
	require.True(t, target.Err().Is(err), "got %v", err)
	var ae appErr.Error
	require.ErrorAs(t, err, &ae)
	require.False(t, ae.StatusCode().IsInternal(), "must be 4xx: %v", err)
}

const previewSpan = `<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent">`

func TestPreviewEmail_UsesSampleValuesAndInlinesLogo(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	out, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType),
		Subject:          "Hi {{.user_first_name}} from {{.event_name}}",
		Preheader:        "Welcome {{.user_first_name}}",
		Body:             json.RawMessage(`[{"type":"logo"},` + string(richTextVarBody("user_last_name"))[1:]),
		Values:           map[string]string{"event_name": "Spring CTF"},
	})
	require.NoError(t, err)

	require.Equal(t, "Hi Jane from Spring CTF", out.Subject, "missing values fall back to type defaults")
	require.Equal(t, "Welcome Jane", out.Preheader)
	require.True(t, strings.Contains(out.HTML, previewSpan+"Welcome Jane</div>"), "preheader span prepended like dispatch: %s", out.HTML)
	require.Contains(t, out.HTML, `src="data:image/png;base64,`)
	require.Contains(t, out.HTML, `src="`+dataURI(branding.LogoContentType, branding.LogoPNG())+`"`)
	require.NotContains(t, out.HTML, "cid:")
	require.NotContains(t, out.HTML, "/api/")
	require.Contains(t, out.HTML, "Doe")
}

func TestPreviewEmail_RejectsInvalidStyling(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	_, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType),
		Subject:          "Hi",
		Body:             json.RawMessage(`[]`),
		Styling:          json.RawMessage(`{"cta_bg_color":"theme:nope"}`),
	})
	require.True(t, notificationModel.ErrInvalidTemplateStyling.Err().Is(err), "got %v", err)
}

func TestPreviewEmail_RejectsTypeWithoutEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	_, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: "no.such.type",
		Body:             json.RawMessage(`[]`),
	})
	require.True(t, notificationModel.ErrInvalidTemplateVariables.Err().Is(err), "got %v", err)
}

// TestPreviewEmail_MatchesDispatch renders one template through the dispatch
// handler (fake mailer) and through PreviewEmail: after mapping every cid:
// source to the data: URI of its inline part the HTML, subject and preheader
// must be equal.
func TestPreviewEmail_MatchesDispatch(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	presetID := uuid.Must(uuid.NewV7())
	subject := "Hello {{.user_first_name}}"
	preheader := "About {{.event_name}}"
	body := `[{"type":"logo","align":"center","width_px":80},` +
		`{"type":"image","file_id":"` + fileID.String() + `","alt":"pic"},` +
		`{"type":"preset","preset_id":"` + presetID.String() + `"},` +
		`{"type":"button","label":"Open","url":"https://example.org/{{.event_tag}}"},` +
		string(richTextVarBody("user_name"))[1:]
	styling := `{"cta_bg_color":"theme:accent","cta_text_color":"theme:on_accent"}`
	presetRow := postgres.NotificationEmailBlockPreset{
		ID:     presetID,
		Blocks: []byte(`[{"type":"logo"},{"type":"divider"}]`),
	}

	vars := map[string]any{}
	for _, d := range notificationTypes.Descriptors(previewType) {
		vars[d.Name] = d.Default
	}

	ctrl := gomock.NewController(t)
	dispatchRepo := postgresMocks.NewMockQuerier(ctrl)
	dispatchRepo.EXPECT().GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationEmailTemplate{
		NotificationType: string(previewType),
		Subject:          subject,
		Preheader:        preheader,
		Body:             []byte(body),
		Styling:          []byte(styling),
	}, nil)
	dispatchRepo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return([]postgres.NotificationEmailBlockPreset{presetRow}, nil)
	m := &fakeMailer{}
	media := &fakeMedia{files: map[uuid.UUID]fakeFile{fileID: {contentType: "image/png", data: []byte("png")}}}
	require.NoError(t, emailUseCase.NewHandler(dispatchRepo, m, media).Handle(
		context.Background(), userModel.User{Email: "a@example.org"}, previewType, vars, nil,
	))

	previewRepo := postgresMocks.NewMockQuerier(ctrl)
	previewRepo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return([]postgres.NotificationEmailBlockPreset{presetRow}, nil)
	previewMedia := newFakeTemplateMedia()
	previewMedia.files[fileID] = mediaModel.File{ID: fileID, ContentType: "image/png", SizeBytes: 3}
	previewMedia.blobs[fileID] = []byte("png")
	out, err := emailUseCase.NewNotificationEmailTemplateUseCase(previewRepo, previewMedia).PreviewEmail(
		context.Background(), emailUseCase.PreviewInput{
			NotificationType: string(previewType),
			Subject:          subject,
			Preheader:        preheader,
			Body:             json.RawMessage(body),
			Styling:          json.RawMessage(styling),
		})
	require.NoError(t, err)

	dispatched := m.last.HTML
	require.Len(t, m.last.Inline, 2)
	for _, part := range m.last.Inline {
		require.Contains(t, dispatched, "cid:"+part.ContentID)
		dispatched = strings.ReplaceAll(dispatched, "cid:"+part.ContentID, dataURI(part.ContentType, part.Data))
	}
	require.Contains(t, out.HTML, dataURI("image/png", []byte("png")))
	require.Equal(t, render.CID(render.Asset{Kind: render.AssetFile, FileID: fileID}), m.last.Inline[1].ContentID)
	require.Equal(t, dispatched, out.HTML)
	require.Equal(t, m.last.Subject, out.Subject)
	require.True(t, strings.Contains(out.HTML, previewSpan+"About Cyber&nbsp;ICE&nbsp;Box CTF</div>"))
	require.Equal(t, "About Cyber\u00a0ICE\u00a0Box CTF", out.Preheader)
}

// TestPreviewEmail_UnrenderableDraftIsInvalidInput: a half-typed draft is
// ordinary preview input → 4xx ErrTemplatePreviewInvalid whose message names
// the renderer's reason, never a masked 500.
func TestPreviewEmail_UnrenderableDraftIsInvalidInput(t *testing.T) {
	cases := map[string]struct {
		in     emailUseCase.PreviewInput
		reason string
	}{
		"subject syntax error": {
			in:     emailUseCase.PreviewInput{Subject: "Hi {{.user_first_name", Body: json.RawMessage(`[]`)},
			reason: "{{.Variable}}",
		},
		"unknown variable in subject": {
			in:     emailUseCase.PreviewInput{Subject: "Hi {{.no_such_var}}", Body: json.RawMessage(`[]`)},
			reason: "no_such_var",
		},
		"unknown variable in preheader": {
			in:     emailUseCase.PreviewInput{Subject: "Hi", Preheader: "{{.nope}}", Body: json.RawMessage(`[]`)},
			reason: "nope",
		},
		"malformed block json": {
			in:     emailUseCase.PreviewInput{Subject: "Hi", Body: json.RawMessage(`[{"type":"rich_text","content":`)},
			reason: "",
		},
		"block array is not an array": {
			in:     emailUseCase.PreviewInput{Subject: "Hi", Body: json.RawMessage(`{"type":"divider"}`)},
			reason: "block array",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil).AnyTimes()
			uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
			tc.in.NotificationType = string(previewType)

			_, err := uc.PreviewEmail(context.Background(), tc.in)
			require.True(t, notificationModel.ErrTemplatePreviewInvalid.Err().Is(err), "got %v", err)
			var ae appErr.Error
			require.ErrorAs(t, err, &ae)
			require.False(t, ae.StatusCode().IsInternal(), "must be 4xx: %v", err)
			msg := ae.StatusCode().Message()
			require.True(t, strings.HasPrefix(msg, "Template cannot be rendered: "), msg)
			require.Contains(t, msg, tc.reason)
		})
	}
}

// A body variable node without a value renders empty in dispatch, so the
// preview renders it empty too rather than rejecting the draft.
func TestPreviewEmail_UnknownBodyVariableRendersEmptyLikeDispatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	_, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType), Subject: "Hi", Body: richTextVarBody("no_such_var"),
	})
	require.NoError(t, err)
}

func TestPreviewEmail_PresetLoadFailureIsPlatformError(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, errors.New("db down"))
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	_, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType), Subject: "Hi", Body: json.RawMessage(`[]`),
	})
	var ae appErr.Error
	require.ErrorAs(t, err, &ae)
	require.True(t, ae.StatusCode().IsInternal(), "infra failure stays 5xx: %v", err)
}

func TestPreviewEmail_InlinesUploadedImage(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	media := newFakeTemplateMedia()
	pngID := media.addBlob("image/png", []byte("png-bytes"))
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, media)

	out, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType), Subject: "Hi", Body: imageBody(pngID.String()),
	})
	require.NoError(t, err)
	require.Contains(t, out.HTML, `src="data:image/png;base64,`)
	require.Contains(t, out.HTML, `src="`+dataURI("image/png", []byte("png-bytes"))+`"`)
	require.NotContains(t, out.HTML, "cid:")
	require.NotContains(t, out.HTML, "/api/")
}

// Preview input is user-typed: an unknown file, a non-image file or images
// over the inline cap are 4xx with a readable message, never a 500.
func TestPreviewEmail_UnusableImagesAreInvalidInput(t *testing.T) {
	media := newFakeTemplateMedia()
	pdfID := media.addBlob("application/pdf", []byte("%PDF"))
	hugeID := media.addBlob("image/png", make([]byte, emailUseCase.MaxInlineBytes))
	cases := map[string]struct {
		body   json.RawMessage
		target appErr.ErrorCreator
	}{
		"missing file":   {imageBody(uuid.Must(uuid.NewV7()).String()), notificationModel.ErrTemplateImageInvalid},
		"non-image file": {imageBody(pdfID.String()), notificationModel.ErrTemplateImageInvalid},
		// the logo (~30 KB) plus a file of exactly MaxInlineBytes is over the cap
		"over inline cap": {json.RawMessage(`[{"type":"logo"},` + string(imageBody(hugeID.String()))[1:]), notificationModel.ErrTemplateInlineTooLarge},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
			uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, media)

			_, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
				NotificationType: string(previewType), Subject: "Hi", Body: tc.body,
			})
			requireInvalidInput(t, tc.target, err)
			var ae appErr.Error
			require.ErrorAs(t, err, &ae)
			require.NotEmpty(t, ae.StatusCode().Message())
		})
	}
}

// failingStreamMedia fails every file read like a storage outage.
type failingStreamMedia struct{ *fakeTemplateMedia }

func (failingStreamMedia) StreamFile(context.Context, uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	return nil, mediaModel.File{}, errors.New("storage down")
}

func TestPreviewEmail_MediaFailureIsPlatformError(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, failingStreamMedia{newFakeTemplateMedia()})

	_, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType), Subject: "Hi", Body: imageBody(uuid.Must(uuid.NewV7()).String()),
	})
	var ae appErr.Error
	require.ErrorAs(t, err, &ae)
	require.True(t, ae.StatusCode().IsInternal(), "infra failure stays 5xx: %v", err)
}
