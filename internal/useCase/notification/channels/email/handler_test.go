package emailUseCase_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/email"
)

// fakeMailer is a hand double for the email handler's send port. It records the
// last sent message and returns a configurable error.
type fakeMailer struct {
	calls       int
	last        email.Message
	lastTo      string
	lastSubject string
	lastBody    string
	lastEvent   *uuid.UUID
	err         error
}

func (f *fakeMailer) Deliver(_ context.Context, eventID *uuid.UUID, msg email.Message) error {
	f.calls++
	f.lastEvent = eventID
	f.last = msg
	f.lastTo, f.lastSubject, f.lastBody = msg.To, msg.Subject, msg.HTML
	return f.err
}

// fakeMedia serves in-memory files by id; unknown ids → ErrFileNotFound.
type fakeMedia struct {
	files map[uuid.UUID]fakeFile
}

type fakeFile struct {
	contentType string
	data        []byte
}

func (f *fakeMedia) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	ff, ok := f.files[id]
	if !ok {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return io.NopCloser(bytes.NewReader(ff.data)), mediaModel.File{ID: id, ContentType: ff.contentType, SizeBytes: int64(len(ff.data))}, nil
}

// richTextVarBody returns a one-block rich_text JSON body whose single paragraph
// contains a variable node resolved from vars[varName].
func richTextVarBody(varName string) []byte {
	return []byte(`[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"variable","varName":"` + varName + `"}]}]}}}]`)
}

func TestEmailHandler_Handle_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	emailRepo := postgresMocks.NewMockQuerier(ctrl)
	m := &fakeMailer{}

	user := userModel.User{Email: "bob@example.com"}
	ctx := context.Background()

	emailRepo.EXPECT().
		GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).
		Return(
			postgres.NotificationEmailTemplate{
				Subject: "Hi {{.Name}}",
				Body:    richTextVarBody("Name"),
			}, nil,
		)
	emailRepo.EXPECT().
		ListEmailBlockPresets(gomock.Any()).
		Return(nil, nil)

	h := emailUseCase.NewHandler(emailRepo, m, &fakeMedia{})
	err := h.Handle(ctx, user, notificationTypes.NotificationTypeFlagAccepted, map[string]any{"Name": "Bob"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, m.calls)
	require.Equal(t, "bob@example.com", m.lastTo)
	require.Equal(t, "Hi Bob", m.lastSubject)
	require.Contains(t, m.lastBody, "Bob")
}

func TestEmailHandler_Handle_NoTemplate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	emailRepo := postgresMocks.NewMockQuerier(ctrl)
	m := &fakeMailer{}

	user := userModel.User{Email: "bob@example.com"}
	ctx := context.Background()

	emailRepo.EXPECT().
		GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationEmailTemplate{}, pgx.ErrNoRows)

	h := emailUseCase.NewHandler(emailRepo, m, &fakeMedia{})
	err := h.Handle(ctx, user, notificationTypes.NotificationTypeFlagAccepted, nil, nil)
	require.Error(t, err)
	require.True(
		t, notificationModel.ErrTemplateNotFound.Err().Is(err),
		"expected ErrTemplateNotFound, got: %v", err,
	)
	require.Equal(t, 0, m.calls, "mailer.Send must not be called when no template")
}

func TestEmailHandler_Handle_Preheader(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	emailRepo := postgresMocks.NewMockQuerier(ctrl)
	m := &fakeMailer{}

	user := userModel.User{Email: "alice@example.com"}
	ctx := context.Background()

	emailRepo.EXPECT().
		GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).
		Return(
			postgres.NotificationEmailTemplate{
				Subject:   "Welcome {{.Name}}",
				Body:      richTextVarBody("Name"),
				Preheader: "preview {{.Name}}",
			}, nil,
		)
	emailRepo.EXPECT().
		ListEmailBlockPresets(gomock.Any()).
		Return(nil, nil)

	h := emailUseCase.NewHandler(emailRepo, m, &fakeMedia{})
	err := h.Handle(ctx, user, notificationTypes.NotificationTypeFlagAccepted, map[string]any{"Name": "Alice"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, m.calls)
	require.Equal(t, "Welcome Alice", m.lastSubject)
	require.Contains(t, m.lastBody, `<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent">preview Alice</div>`)
	require.Contains(t, m.lastBody, "preview Alice")
	require.Contains(t, m.lastBody, "Alice")
}

// TestEmailHandler_Handle_BlockRender verifies that the handler uses RenderEmail
// to convert a JSON block body into HTML, substituting variable nodes from vars.
func TestEmailHandler_Handle_BlockRender(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	emailRepo := postgresMocks.NewMockQuerier(ctrl)
	m := &fakeMailer{}

	user := userModel.User{Email: "ann@example.com"}
	ctx := context.Background()

	// One rich_text block: paragraph with a literal "Hello " text node followed
	// by a variable node that resolves vars["name"].
	body := []byte(`[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hello "},{"type":"variable","varName":"name"}]}]}}}]`)

	emailRepo.EXPECT().
		GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).
		Return(
			postgres.NotificationEmailTemplate{
				Subject: "Greet",
				Body:    body,
			}, nil,
		)
	emailRepo.EXPECT().
		ListEmailBlockPresets(gomock.Any()).
		Return(nil, nil)

	h := emailUseCase.NewHandler(emailRepo, m, &fakeMedia{})
	err := h.Handle(ctx, user, notificationTypes.NotificationTypeFlagAccepted, map[string]any{"name": "Ann"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, m.calls)
	require.Equal(t, "ann@example.com", m.lastTo)
	require.Contains(t, m.lastBody, "Hello Ann", "rendered body should contain substituted text")
}

func TestEmailHandler_Handle_UsesSpecifiedDraftTemplate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := postgresMocks.NewMockQuerier(ctrl)
	mailer := &fakeMailer{}
	templateID := uuid.Must(uuid.NewV4())
	repo.EXPECT().GetEmailTemplate(gomock.Any(), templateID).Return(
		postgres.NotificationEmailTemplate{
			ID:               templateID,
			NotificationType: string(notificationTypes.NotificationTypeFlagAccepted),
			Status:           "draft",
			Subject:          "Draft {{.Name}}",
			Body:             richTextVarBody("Name"),
		}, nil,
	)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)

	err := emailUseCase.NewHandler(repo, mailer, &fakeMedia{}).Handle(
		context.Background(), userModel.User{Email: "preview@example.com"},
		notificationTypes.NotificationTypeFlagAccepted, map[string]any{"Name": "Preview"}, &templateID,
	)
	require.NoError(t, err)
	require.Equal(t, "Draft Preview", mailer.lastSubject)
}

// imageTemplate expects a published template whose body is a logo block plus an
// uploaded-file image block.
func imageTemplate(repo *postgresMocks.MockQuerier, fileID uuid.UUID) {
	body := `[{"type":"logo"},{"type":"image","file_id":"` + fileID.String() + `","alt":"pic"},` +
		`{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Hello & welcome"}]}]}}}]`
	repo.EXPECT().GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationEmailTemplate{Subject: "Hi", Body: []byte(body)}, nil)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
}

func TestEmailHandler_Handle_AttachesInlineImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	fileID := uuid.Must(uuid.NewV4())
	imageTemplate(repo, fileID)
	m := &fakeMailer{}
	media := &fakeMedia{files: map[uuid.UUID]fakeFile{fileID: {contentType: "image/jpeg", data: []byte("jpeg-bytes")}}}

	err := emailUseCase.NewHandler(repo, m, media).Handle(
		context.Background(), userModel.User{Email: "bob@example.com"},
		notificationTypes.NotificationTypeFlagAccepted, nil, nil,
	)
	require.NoError(t, err)
	require.Equal(t, 1, m.calls)

	fileCID := fileID.String() + "@cybericebox"
	require.Contains(t, m.last.HTML, `src="cid:logo@cybericebox"`)
	require.Contains(t, m.last.HTML, `src="cid:`+fileCID+`"`)
	require.Equal(t, []email.InlinePart{
		{ContentID: "logo@cybericebox", ContentType: branding.LogoContentType, Data: branding.LogoPNG()},
		{ContentID: fileCID, ContentType: "image/jpeg", Data: []byte("jpeg-bytes")},
	}, m.last.Inline)
	require.Contains(t, m.last.Text, "Hello & welcome")
	require.NotContains(t, m.last.Text, "<")
}

func TestEmailHandler_Handle_MissingInlineFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	imageTemplate(repo, uuid.Must(uuid.NewV4()))
	m := &fakeMailer{}

	err := emailUseCase.NewHandler(repo, m, &fakeMedia{}).Handle(
		context.Background(), userModel.User{Email: "bob@example.com"},
		notificationTypes.NotificationTypeFlagAccepted, nil, nil,
	)
	require.Error(t, err)
	require.Equal(t, 0, m.calls, "nothing is sent when an inline image cannot be loaded")
}

func TestEmailHandler_Handle_InlineImagesOverLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	fileID := uuid.Must(uuid.NewV4())
	imageTemplate(repo, fileID)
	m := &fakeMailer{}
	// The logo alone is ~30 KB, so a file of exactly MaxInlineBytes pushes the total over.
	media := &fakeMedia{files: map[uuid.UUID]fakeFile{fileID: {contentType: "image/png", data: make([]byte, emailUseCase.MaxInlineBytes)}}}

	err := emailUseCase.NewHandler(repo, m, media).Handle(
		context.Background(), userModel.User{Email: "bob@example.com"},
		notificationTypes.NotificationTypeFlagAccepted, nil, nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Inline images exceed email size limit")
	require.Equal(t, 0, m.calls)
}
