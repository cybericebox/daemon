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
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// city is required and editable, school is required and locked, size is a number.
const prefillFormDocument = `{"blocks":[
	{"id":"city","type":"field","key":"city","input":"text","label":"City","required":true,"editable":true},
	{"id":"school","type":"field","key":"school","input":"text","label":"School","required":true},
	{"id":"size","type":"field","key":"size","input":"number","label":"Size"}]}`

func prefillFormVersion(eventID, versionID uuid.UUID) postgres.EventFormVersion {
	return postgres.EventFormVersion{ID: versionID, FormID: uuid.Must(uuid.NewV7()), EventID: eventID, Version: 1, Enabled: true, Required: true, Document: []byte(prefillFormDocument)}
}

func expectNoStoredAnswers(q *postgresMocks.MockQuerier) {
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
}

func TestInviteParticipantsPrefillsAnswersAndReportsBadValues(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	allowNonStaff(q)
	stubParticipationModelReads(q)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(&invitationNotifier{})
	eventID, managerID, userID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(prefillFormVersion(eventID, versionID), nil)
	expectNoStoredAnswers(q)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), "ok@example.test").Return(postgres.User{ID: userID, Email: "ok@example.test", Status: "active"}, nil)
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 1, Invited: true}, nil)
	q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		require.Equal(t, userID, arg.UserID)
		require.Equal(t, versionID, arg.FormVersionID)
		var saved map[string]any
		require.NoError(t, json.Unmarshal(arg.Answers, &saved))
		require.Equal(t, map[string]any{"school": "KPI", "size": float64(4)}, saved, "the missing required city stays empty")
		return postgres.EventFormAnswer{EventID: eventID, UserID: userID, FormVersionID: versionID, Answers: arg.Answers}, nil
	})
	results, err := uc.InviteParticipants(context.Background(), eventID, managerID, []event.ParticipantInvitationInput{
		{Email: "ok@example.test", Fields: map[string]any{"school": "KPI", "size": float64(4)}},
		{Email: "bad@example.test", Fields: map[string]any{"size": "four"}},
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Empty(t, results[0].Code)
	require.Equal(t, event.InvitationCodeFieldsInvalid, results[1].Code, "a wrong value is reported and nobody is invited for it")
}

func TestSubmitParticipantFormKeepsPrefilledLockedAnswers(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes() // an unfinished event
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(prefillFormVersion(eventID, versionID), nil).AnyTimes()
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return([]postgres.ListLatestRegistrationAnswersForUsersRow{
		{UserID: userID, Answers: []byte(`{"school":"KPI"}`)},
	}, nil).AnyTimes()

	_, err := uc.SubmitParticipantForm(context.Background(), eventID, userID, event.SubmitParticipantFormInput{Answers: map[string]any{"city": "Kyiv", "school": "LNU"}})
	if !errors.Is(err, participantModel.ErrParticipantFieldPrefilled.Err()) {
		t.Fatalf("changing a prefilled locked field must fail: %v", err)
	}

	q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		var saved map[string]any
		require.NoError(t, json.Unmarshal(arg.Answers, &saved))
		require.Equal(t, map[string]any{"city": "Kyiv", "school": "KPI"}, saved, "the locked value is kept even when the form omits it")
		return postgres.EventFormAnswer{EventID: eventID, UserID: userID, FormVersionID: versionID, Answers: arg.Answers, SubmittedAt: arg.SubmittedAt}, nil
	})
	_, err = uc.SubmitParticipantForm(context.Background(), eventID, userID, event.SubmitParticipantFormInput{Answers: map[string]any{"city": "Kyiv"}})
	require.NoError(t, err)
}

func TestAcceptInvitationNeedsPrefilledFormCompleted(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return &recordingSignalPublisher{} }})
	eventID, userID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(prefillFormVersion(eventID, versionID), nil)
	// An organizer's prefill without the required city.
	q.EXPECT().GetEventFormAnswer(gomock.Any(), gomock.Any()).Return(postgres.EventFormAnswer{
		EventID: eventID, UserID: userID, FormVersionID: versionID, Answers: []byte(`{"school":"KPI"}`), SubmittedAt: time.Now(),
	}, nil)
	_, err := uc.AcceptParticipantInvitation(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrParticipantFormRequired.Err()) {
		t.Fatalf("an incomplete prefill must not skip the required form: %v", err)
	}
}

// batchMockWithMinSize is a batch-test mock without the form gate stub, for
// tests that serve their own participant form; teams may have one member.
func batchMockWithMinSize(t *testing.T, eventID uuid.UUID) *postgresMocks.MockQuerier {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	stubParticipationModelReads(q)
	allowNonStaff(q)
	q.EXPECT().LockEventForTeamChange(gomock.Any(), eventID).Return(eventID, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 3, MinTeamSize: pgtype.Int4{Int32: 1, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
	return q
}

// importTeamWithFields imports one team with the given team fields and checks
// the stored fields and the missing-required-field number saved with them.
func importTeamWithFields(t *testing.T, teamFields map[string]any, wantMissing int32) {
	t.Helper()
	eventID, managerID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := batchMockWithMinSize(t, eventID)
	unit := &testUoW{}
	uc := batchUC(q, unit, &invitationNotifier{})
	q.EXPECT().GetEventTeamByName(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(prefillFormVersion(eventID, versionID), nil).AnyTimes()
	// The team form has a required field; the import leaves it empty.
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{
		Version: 1, Enabled: true, Required: true, RequireExisting: true,
		Document: []byte(`{"blocks":[{"id":"uni","type":"field","key":"uni","input":"text","label":"Uni","required":true},{"id":"n","type":"field","key":"n","input":"number","label":"N"}]}`),
	}, nil).AnyTimes()
	expectNoStoredAnswers(q)
	var captainID uuid.UUID
	q.EXPECT().GetUserByEmail(gomock.Any(), "cap@example.test").Return(postgres.User{}, pgx.ErrNoRows)
	q.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateUserParams) (postgres.User, error) {
		captainID = p.ID
		return postgres.User{ID: p.ID, Email: p.Email, Status: p.Status}, nil
	})
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		return postgres.EventTeam{ID: arg.ID, EventID: eventID, Name: arg.Name, CaptainID: arg.CaptainID}, nil
	})
	q.EXPECT().UpdateEventTeamExtraFields(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamExtraFieldsParams) (int64, error) {
		want, err := json.Marshal(teamFields)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(arg.ExtraFields))
		require.Equal(t, pgtype.Int4{Int32: wantMissing, Valid: true}, arg.FieldsMissing, "saved through saveTeamFields with the gap count")
		return 1, nil
	})
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, Status: 1, Invited: true}, nil)
	q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		require.Equal(t, captainID, arg.UserID)
		require.JSONEq(t, `{"school":"KPI"}`, string(arg.Answers))
		return postgres.EventFormAnswer{Answers: arg.Answers}, nil
	})
	result, err := uc.CreateManagedTeams(context.Background(), eventID, managerID, []event.BatchTeamInput{{
		Name: "Blue Team", CaptainEmail: "cap@example.test", Partial: true, Fields: teamFields,
		Members: []event.BatchTeamMemberInput{{Email: "cap@example.test", Fields: map[string]any{"school": "KPI"}}},
	}}, false)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.True(t, unit.saved)
}

func TestCreateManagedTeamsImportSavesPartialTeamAndMemberFields(t *testing.T) {
	// The required team field "uni" is left empty: one gap is stored with the answers.
	importTeamWithFields(t, map[string]any{"n": float64(2)}, 1)
	importTeamWithFields(t, map[string]any{"n": float64(2), "uni": "KPI"}, 0)

	eventID, managerID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	// A wrong member value is an issue that points at the person; nothing is written.
	unit2 := &testUoW{}
	q2 := batchMockWithMinSize(t, eventID)
	q2.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(prefillFormVersion(eventID, versionID), nil).AnyTimes()
	q2.EXPECT().GetEventTeamByName(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	q2.EXPECT().GetUserByEmail(gomock.Any(), "cap2@example.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Email: "cap2@example.test", Status: "active"}, nil)
	q2.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	result, err := batchUC(q2, unit2, &invitationNotifier{}).CreateManagedTeams(context.Background(), eventID, managerID, []event.BatchTeamInput{{
		Name: "Red Team", CaptainEmail: "cap2@example.test", Partial: true,
		Members: []event.BatchTeamMemberInput{{Email: "cap2@example.test", Fields: map[string]any{"size": "big"}}},
	}}, false)
	require.NoError(t, err)
	require.Equal(t, []event.BatchTeamIssue{{Team: 0, Email: "cap2@example.test", Code: event.BatchIssueMemberFields}}, result.Issues)
	require.False(t, unit2.saved)
}

func TestGetOwnParticipantFormAnswersHidesStaffOnlyFields(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	version := prefillFormVersion(eventID, uuid.Must(uuid.NewV7()))
	version.Document = []byte(`{"blocks":[{"id":"city","type":"field","key":"city","input":"text","label":"City"},{"id":"note","type":"field","key":"note","input":"text","label":"Note","staffOnly":true}]}`)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(version, nil)
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return([]postgres.ListLatestRegistrationAnswersForUsersRow{
		{UserID: userID, Answers: []byte(`{"city":"Kyiv","note":"late"}`)},
	}, nil)
	answers, err := uc.GetOwnParticipantFormAnswers(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"city": "Kyiv"}, answers)
}

func TestInviteParticipantsPrefillCountsMissingRequiredFields(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	allowNonStaff(q)
	stubParticipationModelReads(q)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(&invitationNotifier{})
	eventID, managerID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	version := prefillFormVersion(eventID, versionID)
	version.RequireExisting = true // «Вимагати від усіх»: the gaps are counted
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(version, nil)
	expectNoStoredAnswers(q)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil).Times(2)
	users := map[string]uuid.UUID{"gap@example.test": uuid.Must(uuid.NewV7()), "full@example.test": uuid.Must(uuid.NewV7())}
	q.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, email string) (postgres.User, error) {
		return postgres.User{ID: users[email], Email: email, Status: "active"}, nil
	}).Times(2)
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.InviteEventParticipantParams) (postgres.EventParticipant, error) {
		return postgres.EventParticipant{EventID: eventID, UserID: p.UserID, Status: 1, Invited: true}, nil
	}).Times(2)
	q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		return postgres.EventFormAnswer{Answers: arg.Answers}, nil
	}).Times(2)
	missing := map[uuid.UUID]int32{}
	q.EXPECT().SetEventParticipantsFieldsMissing(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.SetEventParticipantsFieldsMissingParams) error {
		for i, id := range arg.UserIds {
			missing[id] = arg.Missing[i]
		}
		return nil
	}).Times(2)
	// city and school are required: one is given for the first person, both for the second.
	results, err := uc.InviteParticipants(context.Background(), eventID, managerID, []event.ParticipantInvitationInput{
		{Email: "gap@example.test", Fields: map[string]any{"school": "KPI"}},
		{Email: "full@example.test", Fields: map[string]any{"school": "KPI", "city": "Kyiv"}},
	})
	require.NoError(t, err)
	require.Empty(t, results[0].Code)
	require.Empty(t, results[1].Code)
	require.Equal(t, map[uuid.UUID]int32{users["gap@example.test"]: 1, users["full@example.test"]: 0}, missing)
}
