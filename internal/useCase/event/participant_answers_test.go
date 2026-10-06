package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

const editableFormDocument = `{"blocks":[
	{"id":"city","type":"field","key":"city","input":"text","label":"City","required":true,"editable":true},
	{"id":"school","type":"field","key":"school","input":"text","label":"School"}]}`

type answersFixture struct {
	q               *postgresMocks.MockQuerier
	uc              *event.EventUseCase
	eventID, userID uuid.UUID
	versionID       uuid.UUID
}

func newAnswersFixture(t *testing.T, status participantModel.Status, finished bool) answersFixture {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	f := answersFixture{q: q, uc: newUC(q), eventID: uuid.Must(uuid.NewV7()), userID: uuid.Must(uuid.NewV7()), versionID: uuid.Must(uuid.NewV7())}
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: f.eventID, UserID: f.userID}).
		Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: int16(status)}, nil)
	if status != participantModel.StatusApproved {
		return f
	}
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), f.eventID).Return(postgres.EventFormVersion{ID: f.versionID, EventID: f.eventID, Version: 3, Enabled: true, Document: []byte(editableFormDocument)}, nil)
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), postgres.ListLatestRegistrationAnswersForUsersParams{EventID: f.eventID, UserIds: []uuid.UUID{f.userID}}).
		Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: f.userID, Answers: []byte(`{"city":"Kyiv","school":"KPI"}`)}}, nil)
	row := startedEvent(f.eventID, time.Now())
	if finished {
		row.FinishAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
	}
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(row, nil)
	return f
}

func TestGetOwnParticipantAnswers(t *testing.T) {
	f := newAnswersFixture(t, participantModel.StatusApproved, false)
	view, err := f.uc.GetOwnParticipantAnswers(context.Background(), f.eventID, f.userID)
	if err != nil || !view.Editable || view.Form.Version != 3 || view.Answers["city"] != "Kyiv" {
		t.Fatalf("answers = %+v, %v", view, err)
	}
	f = newAnswersFixture(t, participantModel.StatusApproved, true)
	if view, err = f.uc.GetOwnParticipantAnswers(context.Background(), f.eventID, f.userID); err != nil || view.Editable {
		t.Fatalf("finished answers = %+v, %v", view, err)
	}
}

func TestGetOwnParticipantAnswersRequiresApprovalAndForm(t *testing.T) {
	f := newAnswersFixture(t, participantModel.StatusPending, false)
	if _, err := f.uc.GetOwnParticipantAnswers(context.Background(), f.eventID, f.userID); !errors.Is(err, participantModel.ErrParticipantAccessForbidden.Err()) {
		t.Fatalf("pending participant: %v", err)
	}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{Status: int16(participantModel.StatusApproved)}, nil)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{}, pgx.ErrNoRows)
	if _, err := newUC(q).GetOwnParticipantAnswers(context.Background(), eventID, userID); !errors.Is(err, eventModel.ErrParticipantFormNotFound.Err()) {
		t.Fatalf("no form: %v", err)
	}
}

func TestUpdateOwnParticipantAnswersMergesEditableFields(t *testing.T) {
	f := newAnswersFixture(t, participantModel.StatusApproved, false)
	f.q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		var saved map[string]any
		if err := json.Unmarshal(arg.Answers, &saved); err != nil {
			t.Fatal(err)
		}
		if arg.FormVersionID != f.versionID || saved["city"] != "Lviv" || saved["school"] != "KPI" {
			t.Fatalf("saved answer = %+v %v", arg, saved)
		}
		return postgres.EventFormAnswer{EventID: arg.EventID, UserID: arg.UserID, FormVersionID: arg.FormVersionID, Answers: arg.Answers, SubmittedAt: arg.SubmittedAt}, nil
	})
	view, err := f.uc.UpdateOwnParticipantAnswers(context.Background(), f.eventID, f.userID, map[string]any{"city": "Lviv", "school": "KPI"})
	if err != nil || view.Answers["city"] != "Lviv" || !view.Editable {
		t.Fatalf("updated = %+v, %v", view, err)
	}
}

func TestUpdateOwnParticipantAnswersRejections(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finished bool
		answers  map[string]any
		want     error
	}{
		{name: "non-editable change", answers: map[string]any{"school": "LNU"}, want: participantModel.ErrParticipantFieldNotEditable.Err()},
		{name: "after the effective finish", finished: true, answers: map[string]any{"city": "Lviv"}, want: participantModel.ErrParticipantFieldsLocked.Err()},
		{name: "invalid merged answers", answers: map[string]any{"city": ""}, want: participantModel.ErrParticipantFieldRequired.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAnswersFixture(t, participantModel.StatusApproved, tc.finished)
			if _, err := f.uc.UpdateOwnParticipantAnswers(context.Background(), f.eventID, f.userID, tc.answers); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
