package event_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

const fileFormDocument = `{"blocks":[
	{"id":"cv","type":"field","key":"cv","input":"file","label":"CV","required":true,"fileTypes":["pdf"],"maxSizeMB":1}]}`

type answerFileFixture struct {
	q                        *postgresMocks.MockQuerier
	uc                       *event.EventUseCase
	media                    *brandMediaFake
	eventID, userID, formVer uuid.UUID
}

func newAnswerFileFixture(t *testing.T) answerFileFixture {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	media := &brandMediaFake{file: mediaModel.File{ID: uuid.Must(uuid.NewV7()), SizeBytes: 9}}
	f := answerFileFixture{q: q, media: media, uc: event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media}),
		eventID: uuid.Must(uuid.NewV7()), userID: uuid.Must(uuid.NewV7()), formVer: uuid.Must(uuid.NewV7())}
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), f.eventID).
		Return(postgres.EventFormVersion{ID: f.formVer, EventID: f.eventID, Version: 1, Enabled: true, Document: []byte(fileFormDocument)}, nil)
	return f
}

func TestUploadAnswerFileChecksContentAndSize(t *testing.T) {
	ctx := context.Background()
	f := newAnswerFileFixture(t)
	f.q.EXPECT().CreateEventAnswerFile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventAnswerFileParams) error {
		if arg.FileID != f.media.file.ID || arg.EventID != f.eventID || arg.Scope != "participant" || arg.FieldKey != "cv" ||
			arg.Name != "cv.pdf" || arg.ContentType != "application/pdf" || arg.UploadedBy != f.userID {
			t.Fatalf("recorded upload: %+v", arg)
		}
		return nil
	})
	view, err := f.uc.UploadAnswerFile(ctx, f.eventID, f.userID, eventFormModel.AnswerScopeParticipant, "cv", `C:\docs\cv.pdf`, strings.NewReader("%PDF-1.7\n"))
	if err != nil || view.ID != f.media.file.ID || view.Name != "cv.pdf" {
		t.Fatalf("upload = %+v, %v", view, err)
	}

	f = newAnswerFileFixture(t)
	_, err = f.uc.UploadAnswerFile(ctx, f.eventID, f.userID, eventFormModel.AnswerScopeParticipant, "cv", "cv.pdf", strings.NewReader("just text, named .pdf"))
	if !errors.Is(err, eventModel.ErrAnswerFileTypeInvalid.Err()) {
		t.Fatalf("a renamed text file must be rejected by content: %v", err)
	}

	f = newAnswerFileFixture(t)
	big := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 1<<20)...)
	_, err = f.uc.UploadAnswerFile(ctx, f.eventID, f.userID, eventFormModel.AnswerScopeParticipant, "cv", "cv.pdf", bytes.NewReader(big))
	if !errors.Is(err, eventModel.ErrAnswerFileTooLarge.Err()) {
		t.Fatalf("a file over the question limit must be rejected: %v", err)
	}

	f = newAnswerFileFixture(t)
	_, err = f.uc.UploadAnswerFile(ctx, f.eventID, f.userID, eventFormModel.AnswerScopeParticipant, "missing", "cv.pdf", strings.NewReader("%PDF-1.7\n"))
	if !errors.Is(err, eventModel.ErrAnswerFileFieldInvalid.Err()) {
		t.Fatalf("only a file question takes files: %v", err)
	}
}

func answerFileRow(f answerFileFixture, fileID, uploader uuid.UUID) postgres.EventAnswerFile {
	return postgres.EventAnswerFile{FileID: fileID, EventID: f.eventID, Scope: "participant", FieldKey: "cv", Name: "cv.pdf", SizeBytes: 9, ContentType: "application/pdf", UploadedBy: uploader, AttachedAt: pgtype.Timestamptz{}}
}

func TestSubmitParticipantFormBindsAndAttachesFiles(t *testing.T) {
	ctx := context.Background()
	f := newAnswerFileFixture(t)
	fileID := uuid.Must(uuid.NewV7())
	f.q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil)
	f.q.EXPECT().ListEventAnswerFiles(gomock.Any(), []uuid.UUID{fileID}).Return([]postgres.EventAnswerFile{answerFileRow(f, fileID, f.userID)}, nil)
	f.q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		var saved map[string]map[string]any
		if err := json.Unmarshal(arg.Answers, &saved); err != nil || saved["cv"]["name"] != "cv.pdf" || saved["cv"]["contentType"] != "application/pdf" {
			t.Fatalf("saved answer must carry the stored file metadata: %s", arg.Answers)
		}
		return postgres.EventFormAnswer{EventID: arg.EventID, UserID: arg.UserID, FormVersionID: arg.FormVersionID, Answers: arg.Answers, SubmittedAt: arg.SubmittedAt}, nil
	})
	f.q.EXPECT().AttachEventAnswerFiles(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.AttachEventAnswerFilesParams) (int64, error) {
		if arg.OwnerID.UUID != f.userID || arg.Scope != "participant" || len(arg.FileIds) != 1 || arg.FileIds[0] != fileID {
			t.Fatalf("attach: %+v", arg)
		}
		return 1, nil
	})
	answers := map[string]any{"cv": map[string]any{"id": fileID.String(), "name": "forged.exe", "size": 1}}
	if _, err := f.uc.SubmitParticipantForm(ctx, f.eventID, f.userID, event.SubmitParticipantFormInput{Answers: answers}); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func TestSubmitParticipantFormRejectsSomeoneElsesUpload(t *testing.T) {
	f := newAnswerFileFixture(t)
	fileID := uuid.Must(uuid.NewV7())
	f.q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil)
	f.q.EXPECT().ListEventAnswerFiles(gomock.Any(), []uuid.UUID{fileID}).Return([]postgres.EventAnswerFile{answerFileRow(f, fileID, uuid.Must(uuid.NewV7()))}, nil)
	answers := map[string]any{"cv": map[string]any{"id": fileID.String()}}
	_, err := f.uc.SubmitParticipantForm(context.Background(), f.eventID, f.userID, event.SubmitParticipantFormInput{Answers: answers})
	if !errors.Is(err, eventModel.ErrAnswerFileUnavailable.Err()) {
		t.Fatalf("another user's upload must be refused: %v", err)
	}
}
