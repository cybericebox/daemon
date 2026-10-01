package event_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/internal/useCase/event"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// templateMediaFake is an in-memory emailUseCase.TemplateMedia for Event
// template tests; it records reference bookkeeping.
type templateMediaFake struct {
	files    map[uuid.UUID]mediaModel.File
	replaced map[uuid.UUID][]uuid.UUID
	removed  []uuid.UUID
}

func newTemplateMediaFake() *templateMediaFake {
	return &templateMediaFake{files: map[uuid.UUID]mediaModel.File{}, replaced: map[uuid.UUID][]uuid.UUID{}}
}

func (f *templateMediaFake) addFile(size int64) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.files[id] = mediaModel.File{ID: id, ContentType: "image/png", SizeBytes: size}
	return id
}

func (f *templateMediaFake) UploadFile(_ context.Context, name, contentType string, reader io.Reader, _ uuid.UUID) (mediaModel.File, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return mediaModel.File{}, err
	}
	id := uuid.Must(uuid.NewV7())
	file := mediaModel.File{ID: id, Name: name, ContentType: contentType, SizeBytes: int64(len(data))}
	f.files[id] = file
	return file, nil
}

func (f *templateMediaFake) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	file, ok := f.files[id]
	if !ok {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return io.NopCloser(strings.NewReader("img")), file, nil
}

func (f *templateMediaFake) GetFile(_ context.Context, id uuid.UUID) (mediaModel.File, error) {
	file, ok := f.files[id]
	if !ok {
		return mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return file, nil
}

func (f *templateMediaFake) ReplaceReferences(_ context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error {
	if refType != mediaModel.RefTypeEmailTemplate {
		return errors.New("unexpected ref type " + refType)
	}
	f.replaced[refID] = fileIDs
	return nil
}

func (f *templateMediaFake) AddReference(_ context.Context, refType string, refID, fileID uuid.UUID) error {
	if refType != mediaModel.RefTypeEmailTemplate {
		return errors.New("unexpected ref type " + refType)
	}
	f.replaced[refID] = append(f.replaced[refID], fileID)
	return nil
}

func (f *templateMediaFake) RemoveReferences(_ context.Context, refType string, refID uuid.UUID) error {
	if refType != mediaModel.RefTypeEmailTemplate {
		return errors.New("unexpected ref type " + refType)
	}
	f.removed = append(f.removed, refID)
	return nil
}

var _ emailUseCase.TemplateMedia = (*templateMediaFake)(nil)

func newTemplateUC(q event.IRepository, m *templateMediaFake) *event.EventUseCase {
	return event.NewEventUseCase(event.Dependencies{Repo: q, Media: m})
}

func TestUploadEventEmailImage_AttachesOnlyToOwnedDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	u := newTemplateUC(q, m)
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	draft := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), draft.ID).Return(draft, nil)

	file, err := u.UploadEventEmailImage(context.Background(), eventID, draft.ID, actor, bytes.NewReader(branding.LogoPNG()), "banner.png")
	require.NoError(t, err)
	require.Equal(t, "image/png", file.ContentType)
	require.Equal(t, []uuid.UUID{file.ID}, m.replaced[draft.ID])

	foreignEventID := uuid.Must(uuid.NewV7())
	foreign := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &foreignEventID, now)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), foreign.ID).Return(foreign, nil)
	_, err = u.UploadEventEmailImage(context.Background(), eventID, foreign.ID, actor, bytes.NewReader(branding.LogoPNG()), "foreign.png")
	require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err), "%v", err)
	require.Len(t, m.files, 1)
}

func imageBlocks(ids ...uuid.UUID) []byte {
	out := "["
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += `{"type":"image","file_id":"` + id.String() + `"}`
	}
	return []byte(out + "]")
}

// echoCreatedEmailTemplate returns the written row as the created one.
func echoCreatedEmailTemplate(now time.Time) func(context.Context, postgres.CreateEmailTemplateParams) (postgres.NotificationEmailTemplate, error) {
	return func(_ context.Context, arg postgres.CreateEmailTemplateParams) (postgres.NotificationEmailTemplate, error) {
		return postgres.NotificationEmailTemplate{
			ID: arg.ID, NotificationType: arg.NotificationType, Status: arg.Status, Subject: arg.Subject,
			Preheader: arg.Preheader, Body: arg.Body, Styling: arg.Styling, ScopeEventID: arg.ScopeEventID,
			CreatedAt: now, UpdatedAt: now,
		}, nil
	}
}

func TestCreateEventEmailTemplate_SetsImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	file := m.addFile(10)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	expectFileUsable(q, file, eventID, true)
	q.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Any()).DoAndReturn(echoCreatedEmailTemplate(now))

	got, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved", Subject: "S", Body: imageBlocks(file), Styling: []byte("{}"),
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file}, m.replaced[got.ID])
}

func TestCreateEventEmailTemplate_RejectsUnknownImageFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)

	_, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved", Subject: "S", Body: imageBlocks(uuid.Must(uuid.NewV7())), Styling: []byte("{}"),
	})
	require.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}

func TestUpdateEventEmailTemplate_ReplacesImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	file := m.addFile(10)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	updated := row
	updated.Body = imageBlocks(file)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)
	expectFileUsable(q, file, eventID, true)
	q.EXPECT().UpdateEmailTemplate(gomock.Any(), gomock.Any()).Return(updated, nil)

	_, err := uc.UpdateEventEmailTemplate(context.Background(), eventID, emailModel.UpdateTemplateInput{
		ID: row.ID, Subject: "S", Body: imageBlocks(file), Styling: []byte("{}"),
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file}, m.replaced[row.ID])
}

// expectFileUsable expects the Event image ownership check for one file.
func expectFileUsable(q *postgresMocks.MockQuerier, fileID, eventID uuid.UUID, usable bool) {
	q.EXPECT().EmailTemplateFileUsableByEvent(gomock.Any(), postgres.EmailTemplateFileUsableByEventParams{
		TemplateRefType: mediaModel.RefTypeEmailTemplate,
		PresetRefType:   mediaModel.RefTypeEmailBlockPreset,
		FileID:          fileID,
		EventID:         eventID,
	}).Return(usable, nil)
}

// An existing PNG that no platform template, preset or template of THIS
// Event uses (another user's avatar, an exercise image, another Event's
// template image) must not become an Event template image.
func TestCreateEventEmailTemplate_RejectsImageNotUsableByEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl) // CreateEmailTemplate must NOT be called
	m := newTemplateMediaFake()
	foreign := m.addFile(10)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	expectFileUsable(q, foreign, eventID, false)

	_, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved", Subject: "S", Body: imageBlocks(foreign), Styling: []byte("{}"),
	})
	require.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
	require.Empty(t, m.replaced)
}

func TestUpdateEventEmailTemplate_RejectsNewImageNotUsableByEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl) // UpdateEmailTemplate must NOT be called
	m := newTemplateMediaFake()
	foreign := m.addFile(10)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)
	expectFileUsable(q, foreign, eventID, false)

	_, err := uc.UpdateEventEmailTemplate(context.Background(), eventID, emailModel.UpdateTemplateInput{
		ID: row.ID, Subject: "S", Body: imageBlocks(foreign), Styling: []byte("{}"),
	})
	require.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}

// Images the row already holds (e.g. copied from the platform by customize)
// are not re-checked; only the ones the write adds are.
func TestUpdateEventEmailTemplate_ChecksOnlyNewlyAddedImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	kept := m.addFile(10)
	added := m.addFile(10)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	row.Body = imageBlocks(kept)
	updated := row
	updated.Body = imageBlocks(kept, added)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)
	expectFileUsable(q, added, eventID, true) // no call for kept
	q.EXPECT().UpdateEmailTemplate(gomock.Any(), gomock.Any()).Return(updated, nil)

	_, err := uc.UpdateEventEmailTemplate(context.Background(), eventID, emailModel.UpdateTemplateInput{
		ID: row.ID, Subject: "S", Body: imageBlocks(kept, added), Styling: []byte("{}"),
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{kept, added}, m.replaced[row.ID])
}

func TestUpdateEventEmailTemplate_RejectsUnparsableFileID(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

	_, err := uc.UpdateEventEmailTemplate(context.Background(), eventID, emailModel.UpdateTemplateInput{
		ID: row.ID, Subject: "S", Body: []byte(`[{"type":"image","file_id":"nope"}]`), Styling: []byte("{}"),
	})
	require.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}

func TestDeleteEventEmailTemplate_RemovesImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)
	q.EXPECT().DeleteEmailTemplate(gomock.Any(), row.ID).Return(int64(1), nil)

	require.NoError(t, uc.DeleteEventEmailTemplate(context.Background(), eventID, row.ID))
	require.Equal(t, []uuid.UUID{row.ID}, m.removed)
}

func TestRollbackEventEmailTemplate_SetsDraftImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	file := uuid.Must(uuid.NewV7())
	source := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusUnpublished, &eventID, now)
	draft := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	draft.Body = imageBlocks(file)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), source.ID).Return(source, nil)
	q.EXPECT().RollbackEmailTemplate(gomock.Any(), gomock.Any()).Return(draft, nil)

	_, err := uc.RollbackEventEmailTemplate(context.Background(), eventID, source.ID, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file}, m.replaced[draft.ID])
}

func TestCustomizeEventEmailTemplate_SetsImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	file := m.addFile(10)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)
	platform.Body = imageBlocks(file)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	expectNoEmailOverride(q, eventID, platform.NotificationType)
	q.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Any()).DoAndReturn(echoCreatedEmailTemplate(now))

	got, err := uc.CustomizeEventEmailTemplate(context.Background(), eventID, platform.ID, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file}, m.replaced[got.ID])
}

func TestPublishEventEmailTemplate_RejectsInlinePayloadOverLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl) // PublishEmailTemplate must NOT be called
	m := newTemplateMediaFake()
	a := m.addFile(emailUseCase.MaxInlineBytes / 2)
	b := m.addFile(emailUseCase.MaxInlineBytes / 2)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	row.Body = []byte(`[{"type":"logo"},` + string(imageBlocks(a, b)[1:]))

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

	_, err := uc.PublishEventEmailTemplate(context.Background(), eventID, row.ID, uuid.Must(uuid.NewV7()))
	require.True(t, notificationModel.ErrTemplateInlineTooLarge.Err().Is(err), "%v", err)
}
