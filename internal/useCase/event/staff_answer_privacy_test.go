package event_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

const privacyFormDocument = `{"blocks":[
	{"id":"city","type":"field","key":"city","input":"text","label":"City"},
	{"id":"note","type":"field","key":"note","input":"long_text","label":"Note","staffOnly":true}]}`

const historicalStaffAnswers = `{"city":"Kyiv","note":"Current internal note","removed_note":"Historical internal note"}`

// Removing a staff question from the schema leaves its stored values available
// to organizers. Participant reads may only project the current public fields.
func TestParticipantReadsHideRemovedStaffAnswers(t *testing.T) {
	for _, approved := range []bool{false, true} {
		name := "registration prefill"
		if approved {
			name = "approved participant answers"
		}
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			uc := newUC(q)
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{
				ID: uuid.Must(uuid.NewV7()), EventID: eventID, Version: 2, Enabled: true, Document: []byte(privacyFormDocument),
			}, nil)
			q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), postgres.ListLatestRegistrationAnswersForUsersParams{EventID: eventID, UserIds: []uuid.UUID{userID}}).
				Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: userID, Answers: []byte(historicalStaffAnswers)}}, nil)
			var answers map[string]any
			var err error
			if approved {
				q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
					Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil)
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)
				var view event.OwnParticipantAnswersView
				view, err = uc.GetOwnParticipantAnswers(context.Background(), eventID, userID)
				answers = view.Answers
			} else {
				answers, err = uc.GetOwnParticipantFormAnswers(context.Background(), eventID, userID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(answers, map[string]any{"city": "Kyiv"}) {
				t.Fatalf("participant answers = %v, want only the current public answer", answers)
			}
		})
	}
}

func TestParticipantPrefillReadHidesAnswersWithoutCurrentFields(t *testing.T) {
	for _, absent := range []bool{false, true} {
		name := "empty schema"
		if absent {
			name = "no form"
		}
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).
				Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: userID, Answers: []byte(historicalStaffAnswers)}}, nil)
			if absent {
				q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{}, pgx.ErrNoRows)
			} else {
				q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{Document: []byte(`{"blocks":[]}`)}, nil)
			}
			answers, err := newUC(q).GetOwnParticipantFormAnswers(context.Background(), eventID, userID)
			if err != nil {
				t.Fatal(err)
			}
			if len(answers) != 0 {
				t.Fatalf("participant answers without current fields = %v, want none", answers)
			}
		})
	}
}

func TestOwnTeamReadHidesRemovedStaffAnswers(t *testing.T) {
	for _, configured := range []bool{true, false} {
		name := "current form"
		if !configured {
			name = "no current form"
		}
		t.Run(name, func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			uc := newUC(q)
			eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, nil).AnyTimes()
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
				EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
				TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true},
			}, nil)
			q.EXPECT().GetEventTeamForParticipant(gomock.Any(), postgres.GetEventTeamForParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventTeam{
				ID: teamID, EventID: eventID, Name: "Blue", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 2, CreatedAt: now, UpdatedAt: now,
			}, nil)
			q.EXPECT().GetEventTeamExtraFields(gomock.Any(), postgres.GetEventTeamExtraFieldsParams{EventID: eventID, TeamID: teamID}).Return([]byte(historicalStaffAnswers), nil)
			want := map[string]any{}
			if configured {
				q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{EventID: eventID, Enabled: true, Document: []byte(privacyFormDocument)}, nil)
				want["city"] = "Kyiv"
			} else {
				q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{}, pgx.ErrNoRows)
			}
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
			view, err := uc.GetOwnTeam(context.Background(), eventID, userID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(view.ExtraFields, want) {
				t.Fatalf("participant team fields = %v, want %v", view.ExtraFields, want)
			}
		})
	}
}

func TestManagementAnswerReadsKeepCurrentAndHistoricalStaffValues(t *testing.T) {
	want := map[string]any{"city": "Kyiv", "note": "Current internal note", "removed_note": "Historical internal note"}
	t.Run("participant answer history", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		eventID, formID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{FormID: formID, Document: []byte(privacyFormDocument)}, nil)
		q.EXPECT().ListEventFormAnswers(gomock.Any(), postgres.ListEventFormAnswersParams{EventID: eventID, FormID: formID}).Return([]postgres.ListEventFormAnswersRow{{
			EventID: eventID, FormID: formID, UserID: userID, Version: 1, Answers: []byte(historicalStaffAnswers),
			Document: []byte(`{"blocks":[{"id":"removed_note","type":"field","key":"removed_note","input":"long_text","label":"Historical note","staffOnly":true}]}`),
		}}, nil)
		views, err := newUC(q).ListParticipantFormAnswers(context.Background(), eventID)
		if err != nil {
			t.Fatal(err)
		}
		if len(views) != 1 || !reflect.DeepEqual(views[0].Answers, want) || len(views[0].Document.Blocks) != 1 || !views[0].Document.Blocks[0].StaffOnly {
			t.Fatalf("management answer history = %+v", views)
		}
	})
	t.Run("team management", func(t *testing.T) {
		q := newFormGateMock(gomock.NewController(t))
		eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		now := time.Now()
		q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, nil).AnyTimes()
		q.EXPECT().ListEventTeams(gomock.Any(), gomock.Any()).Return([]postgres.ListEventTeamsRow{{EventTeam: postgres.EventTeam{
			ID: teamID, EventID: eventID, Name: "Blue", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 1, CreatedAt: now, UpdatedAt: now, ExtraFields: []byte(historicalStaffAnswers),
		}}}, nil)
		q.EXPECT().CountEventTeamsFiltered(gomock.Any(), postgres.CountEventTeamsFilteredParams{EventID: eventID, AdmissionFilter: -1, FieldFilters: []byte("[]")}).Return(int64(1), nil)
		view, err := newUC(q).ListTeams(context.Background(), event.ListTeamsFilter{EventID: eventID})
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Teams) != 1 || !reflect.DeepEqual(view.Teams[0].ExtraFields, want) {
			t.Fatalf("management team fields = %+v", view.Teams)
		}
	})
}
