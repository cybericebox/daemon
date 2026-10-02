package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func boolPtr(v bool) *bool { return &v }

// policyDocument has one old required question and one new required one.
var policyDocument = eventContentModel.Document{Blocks: []eventContentModel.Block{
	{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City", Required: true},
	{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School", Required: true},
}}

type policyFixture struct {
	q         *postgresMocks.MockQuerier
	uc        *event.EventUseCase
	eventID   uuid.UUID
	formID    uuid.UUID
	versionID uuid.UUID
}

func newPolicyFixture(t *testing.T, previous postgres.EventFormVersion) policyFixture {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	f := policyFixture{q: q, uc: newUC(q), eventID: uuid.Must(uuid.NewV7()), formID: uuid.Must(uuid.NewV7()), versionID: uuid.Must(uuid.NewV7())}
	previous.ID, previous.FormID, previous.EventID = f.versionID, f.formID, f.eventID
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), f.eventID).Return(previous, nil).AnyTimes()
	return f
}

func documentJSON(t *testing.T, doc eventContentModel.Document) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// expectRecount answers the recount of every participant and returns what was
// stored, keyed by user, once the call happened.
func (f policyFixture) expectRecount(t *testing.T, answers map[uuid.UUID]string) map[uuid.UUID]int32 {
	t.Helper()
	rows := make([]postgres.ListEventParticipantRegistrationAnswersRow, 0, len(answers))
	for user, raw := range answers {
		row := postgres.ListEventParticipantRegistrationAnswersRow{UserID: user}
		if raw != "" {
			row.Answers = []byte(raw)
		}
		rows = append(rows, row)
	}
	f.q.EXPECT().ListEventParticipantRegistrationAnswers(gomock.Any(), f.eventID).Return(rows, nil)
	stored := map[uuid.UUID]int32{}
	if len(answers) == 0 {
		f.q.EXPECT().CountEventRegistrationAnswers(gomock.Any(), f.eventID).Return(int64(0), nil)
		f.q.EXPECT().UpdateEventFormSettings(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		return stored
	}
	f.q.EXPECT().SetEventParticipantsFieldsMissing(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.SetEventParticipantsFieldsMissingParams) error {
		if arg.EventID != f.eventID || len(arg.UserIds) != len(arg.Missing) {
			t.Fatalf("recount params = %+v", arg)
		}
		for i, id := range arg.UserIds {
			stored[id] = arg.Missing[i]
		}
		return nil
	})
	f.q.EXPECT().CountEventRegistrationAnswers(gomock.Any(), f.eventID).Return(int64(len(answers)), nil)
	f.q.EXPECT().UpdateEventFormSettings(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	return stored
}

func TestConfigureParticipantFormRequireExistingCountsWhoMustFill(t *testing.T) {
	f := newPolicyFixture(t, postgres.EventFormVersion{Version: 1, Enabled: true, Required: true, Document: documentJSON(t, policyDocument)})
	filled, gap, none := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	stored := f.expectRecount(t, map[uuid.UUID]string{filled: `{"city":"Kyiv","school":"KPI"}`, gap: `{"city":"Lviv"}`, none: ""})
	f.q.EXPECT().CreateEventFormVersion(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventFormVersionParams) (postgres.EventFormVersion, error) {
		if arg.Version != 2 || !arg.RequireExisting || !arg.BlockSubmissions {
			t.Fatalf("version policy = %+v", arg)
		}
		return postgres.EventFormVersion{ID: arg.ID, FormID: arg.FormID, EventID: arg.EventID, Version: arg.Version, Enabled: arg.Enabled, Required: arg.Required,
			Document: arg.Document, CreatedAt: arg.CreatedAt, RequireExisting: arg.RequireExisting, BlockSubmissions: arg.BlockSubmissions}, nil
	})

	view, err := f.uc.ConfigureParticipantForm(context.Background(), f.eventID, event.ConfigureParticipantFormInput{
		Enabled: true, Required: true, Document: policyDocument, RequireExisting: boolPtr(true), BlockSubmissions: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !view.RequireExisting || !view.BlockSubmissions || view.Answered != 3 {
		t.Fatalf("view = %+v", view)
	}
	if stored[filled] != 0 || stored[gap] != 1 || stored[none] != 2 {
		t.Fatalf("missing counts = %v, want 0/1/2", stored)
	}
}

func TestConfigureParticipantFormKeepsThePreviousPolicyUnlessChosen(t *testing.T) {
	previous := postgres.EventFormVersion{Version: 4, Enabled: true, Required: true, RequireExisting: true, BlockSubmissions: true, Document: documentJSON(t, policyDocument)}
	for _, tc := range []struct {
		name                       string
		existing, block            *bool
		wantExisting, wantBlocking bool
	}{
		{name: "nothing chosen keeps both", wantExisting: true, wantBlocking: true},
		{name: "only new registrations", existing: boolPtr(false), wantExisting: false, wantBlocking: false},
		{name: "everyone without blocking", block: boolPtr(false), wantExisting: true, wantBlocking: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFixture(t, previous)
			f.expectRecount(t, map[uuid.UUID]string{})
			f.q.EXPECT().CreateEventFormVersion(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventFormVersionParams) (postgres.EventFormVersion, error) {
				if arg.RequireExisting != tc.wantExisting || arg.BlockSubmissions != tc.wantBlocking {
					t.Fatalf("policy = %v/%v, want %v/%v", arg.RequireExisting, arg.BlockSubmissions, tc.wantExisting, tc.wantBlocking)
				}
				return postgres.EventFormVersion{ID: arg.ID, FormID: arg.FormID, EventID: arg.EventID, Version: arg.Version, Document: arg.Document,
					RequireExisting: arg.RequireExisting, BlockSubmissions: arg.BlockSubmissions}, nil
			})
			if _, err := f.uc.ConfigureParticipantForm(context.Background(), f.eventID, event.ConfigureParticipantFormInput{
				Enabled: true, Required: true, Document: policyDocument, RequireExisting: tc.existing, BlockSubmissions: tc.block,
			}); err != nil {
				t.Fatalf("configure: %v", err)
			}
		})
	}
}

func TestGetOwnParticipantAnswersListsWhatIsStillMissing(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{
		ID: versionID, EventID: eventID, Version: 2, Enabled: true, Required: true, RequireExisting: true, BlockSubmissions: true, Document: documentJSON(t, policyDocument),
	}, nil)
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: userID, Answers: []byte(`{"city":"Kyiv"}`)}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)

	view, err := uc.GetOwnParticipantAnswers(context.Background(), eventID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Missing) != 1 || view.Missing[0] != "school" || !view.Blocking {
		t.Fatalf("missing = %v blocking = %v", view.Missing, view.Blocking)
	}
}

func TestOwnParticipantAnswersNeverCarryStaffOnlyFields(t *testing.T) {
	doc := eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City", Editable: true},
		{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: "Internal note", StaffOnly: true},
	}}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil).Times(2)
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{ID: versionID, EventID: eventID, Version: 1, Enabled: true, Document: documentJSON(t, doc)}, nil).Times(2)
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: userID, Answers: []byte(`{"city":"Kyiv","note":"VIP guest"}`)}}, nil).Times(2)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).Times(2)

	view, err := uc.GetOwnParticipantAnswers(context.Background(), eventID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := view.Answers["note"]; leaked || view.Answers["city"] != "Kyiv" {
		t.Fatalf("answers = %v", view.Answers)
	}
	for _, block := range view.Form.Document.Blocks {
		if block.Key == "note" {
			t.Fatalf("the form carries a staff-only question: %+v", block)
		}
	}
	raw, _ := json.Marshal(view)
	if containsAny(string(raw), "VIP guest", "Internal note") {
		t.Fatalf("staff-only data leaked into the participant view: %s", raw)
	}

	// Saving their own answers must neither leak nor erase what staff wrote.
	q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		var saved map[string]any
		_ = json.Unmarshal(arg.Answers, &saved)
		if saved["note"] != "VIP guest" || saved["city"] != "Lviv" {
			t.Fatalf("saved answers = %v: the recorded staff note must survive", saved)
		}
		return postgres.EventFormAnswer{EventID: arg.EventID, UserID: arg.UserID, FormVersionID: arg.FormVersionID, Answers: arg.Answers, SubmittedAt: arg.SubmittedAt}, nil
	})
	updated, err := uc.UpdateOwnParticipantAnswers(context.Background(), eventID, userID, map[string]any{"city": "Lviv", "note": "I am the boss"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, leaked := updated.Answers["note"]; leaked {
		t.Fatalf("update response leaked a staff-only answer: %v", updated.Answers)
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		for i := 0; i+len(needle) <= len(text); i++ {
			if text[i:i+len(needle)] == needle {
				return true
			}
		}
	}
	return false
}

func TestSubmitParticipantFormKeepsStaffNotesAndIgnoresForgedOnes(t *testing.T) {
	doc := eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City", Required: true},
		{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: "Note", StaffOnly: true, Required: true},
	}}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes() // an unfinished event
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{ID: versionID, EventID: eventID, Version: 3, Enabled: true, Required: true, Document: documentJSON(t, doc)}, nil)
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: userID, Answers: []byte(`{"note":"VIP guest"}`)}}, nil)
	q.EXPECT().UpsertEventFormAnswer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error) {
		var saved map[string]any
		_ = json.Unmarshal(arg.Answers, &saved)
		if saved["note"] != "VIP guest" || saved["city"] != "Kyiv" {
			t.Fatalf("saved = %v", saved)
		}
		return postgres.EventFormAnswer{EventID: arg.EventID, UserID: arg.UserID, FormVersionID: arg.FormVersionID, Answers: arg.Answers, SubmittedAt: arg.SubmittedAt}, nil
	})
	view, err := uc.SubmitParticipantForm(context.Background(), eventID, userID, event.SubmitParticipantFormInput{Answers: map[string]any{"city": "Kyiv", "note": "forged"}})
	if err != nil {
		t.Fatalf("a staff-only required field must not block the participant: %v", err)
	}
	if _, leaked := view.Answers["note"]; leaked {
		t.Fatalf("submit response leaked a staff-only answer: %v", view.Answers)
	}
}

// Staff edit: only the staff-only keys change, the audit records who, and the
// participant's own answers stay as they are.
func TestUpdateParticipantStaffFieldsAuditsAndKeepsOtherAnswers(t *testing.T) {
	doc := eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City"},
		{ID: "came", Type: eventContentModel.BlockField, Key: "came", Input: "checkbox", Label: "Came", StaffOnly: true},
		{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: "Note", StaffOnly: true},
	}}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID, actorID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	answerVersion := uuid.Must(uuid.NewV7())
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{ID: versionID, EventID: eventID, Version: 3, Enabled: true, Document: documentJSON(t, doc)}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil)
	q.EXPECT().GetLatestEventRegistrationAnswerRow(gomock.Any(), postgres.GetLatestEventRegistrationAnswerRowParams{EventID: eventID, UserID: userID}).
		Return(postgres.GetLatestEventRegistrationAnswerRowRow{FormVersionID: answerVersion, Answers: []byte(`{"city":"Kyiv","note":"old"}`)}, nil)
	q.EXPECT().UpdateEventFormAnswerValues(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventFormAnswerValuesParams) (int64, error) {
		var saved map[string]any
		_ = json.Unmarshal(arg.Answers, &saved)
		if arg.FormVersionID != answerVersion || saved["city"] != "Kyiv" || saved["came"] != true || saved["note"] != nil {
			t.Fatalf("saved = %v on %v", saved, arg.FormVersionID)
		}
		return 1, nil
	})
	q.EXPECT().InsertEventStaffFieldChange(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.InsertEventStaffFieldChangeParams) error {
		keys := append([]string(nil), arg.FieldKeys...)
		sort.Strings(keys)
		if arg.Scope != "participant" || arg.SubjectID != userID || arg.ActorID.UUID != actorID || len(keys) != 2 || keys[0] != "came" || keys[1] != "note" {
			t.Fatalf("audit = %+v", arg)
		}
		return nil
	})
	q.EXPECT().GetLastEventStaffFieldChange(gomock.Any(), gomock.Any()).Return(postgres.GetLastEventStaffFieldChangeRow{}, pgx.ErrNoRows)

	view, err := uc.UpdateParticipantStaffFields(context.Background(), eventID, userID, actorID, map[string]any{"came": true, "note": ""})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if view.Values["came"] != true || len(view.Values) != 1 {
		t.Fatalf("values = %v", view.Values)
	}
}

func TestUpdateParticipantStaffFieldsRefusesParticipantQuestions(t *testing.T) {
	doc := eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City"},
		{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: "Note", StaffOnly: true},
	}}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{EventID: eventID, Version: 1, Enabled: true, Document: documentJSON(t, doc)}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID}, nil)
	_, err := uc.UpdateParticipantStaffFields(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()), map[string]any{"city": "Hacked"})
	if !errors.Is(err, participantModel.ErrParticipantStaffFieldsInvalid.Err()) {
		t.Fatalf("a participant question must be refused: %v", err)
	}
}

var _ = pgtype.Timestamptz{}

var teamPolicyDocument = eventContentModel.Document{Blocks: []eventContentModel.Block{
	{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School", Required: true},
	{ID: "motto", Type: eventContentModel.BlockField, Key: "motto", Input: "text", Label: "Motto", Required: true},
	{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: "Internal note", StaffOnly: true},
}}

func teamConfigRow(t *testing.T, eventID uuid.UUID, requireExisting, block bool) postgres.EventTeamFieldConfig {
	return postgres.EventTeamFieldConfig{
		EventID: eventID, Version: 2, Enabled: true, Required: true, RequireExisting: requireExisting, BlockSubmissions: block, Document: documentJSON(t, teamPolicyDocument),
	}
}

func TestGetOwnTeamHidesStaffFieldsAndListsWhatIsMissing(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	uc := newUC(q)
	eventID, teamID, userID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
		TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamForParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now}, nil)
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{"school":"KPI","note":"VIP team"}`), nil)
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(teamConfigRow(t, eventID, true, true), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)

	view, err := uc.GetOwnTeam(context.Background(), eventID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := view.ExtraFields["note"]; leaked || view.ExtraFields["school"] != "KPI" {
		t.Fatalf("team fields = %v", view.ExtraFields)
	}
	if len(view.MissingFields) != 1 || view.MissingFields[0] != "motto" || !view.BlockingFields {
		t.Fatalf("missing = %v blocking = %v", view.MissingFields, view.BlockingFields)
	}
}

// A captain fills a new required field once even when it is not editable;
// the staff note recorded on the team survives and is never overwritten.
func TestUpdateOwnTeamFieldsFillsNewRequiredFieldOnceAndKeepsStaffNotes(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).AnyTimes()
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now}, nil).AnyTimes()
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(teamConfigRow(t, eventID, true, false), nil).AnyTimes()
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{"school":"KPI","note":"VIP team"}`), nil).AnyTimes()
	q.EXPECT().UpdateEventTeamExtraFields(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamExtraFieldsParams) (int64, error) {
		var saved map[string]any
		_ = json.Unmarshal(arg.ExtraFields, &saved)
		if saved["motto"] != "go" || saved["school"] != "KPI" || saved["note"] != "VIP team" {
			t.Fatalf("saved = %v", saved)
		}
		if !arg.FieldsMissing.Valid || arg.FieldsMissing.Int32 != 0 {
			t.Fatalf("a complete team must be stored as complete: %+v", arg.FieldsMissing)
		}
		return 1, nil
	})
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: captainID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
		TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, CreatedAt: now,
	}, nil).AnyTimes()
	q.EXPECT().GetEventTeamForParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now}, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil).AnyTimes()

	// note is staff-only: the captain cannot touch it, and it stays untouched.
	if _, err := uc.UpdateOwnTeamFields(context.Background(), eventID, teamID, captainID, map[string]any{"note": "hacked"}); err == nil {
		t.Fatal("a captain must not write a staff-only field")
	}
	// school is filled and not editable: locked. motto is empty: fill once.
	if _, err := uc.UpdateOwnTeamFields(context.Background(), eventID, teamID, captainID, map[string]any{"school": "LNU"}); err == nil {
		t.Fatal("a filled non-editable field must stay locked")
	}
	view, err := uc.UpdateOwnTeamFields(context.Background(), eventID, teamID, captainID, map[string]any{"motto": "go"})
	if err != nil {
		t.Fatalf("filling the new field once: %v", err)
	}
	if _, leaked := view.ExtraFields["note"]; leaked {
		t.Fatalf("the response leaked a staff-only field: %v", view.ExtraFields)
	}
}

func TestConfigureTeamFieldsRecountsEveryTeam(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	complete, gap := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(teamConfigRow(t, eventID, true, true), nil)
	q.EXPECT().UpsertEventTeamFieldConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventTeamFieldConfigParams) (postgres.EventTeamFieldConfig, error) {
		if !arg.RequireExisting || !arg.BlockSubmissions {
			t.Fatalf("an unchosen policy must be inherited: %+v", arg)
		}
		return postgres.EventTeamFieldConfig{EventID: eventID, Version: 3, Enabled: arg.Enabled, Required: arg.Required, Document: arg.Document, RequireExisting: arg.RequireExisting, BlockSubmissions: arg.BlockSubmissions}, nil
	})
	q.EXPECT().ListEventTeamExtraFields(gomock.Any(), eventID).Return([]postgres.ListEventTeamExtraFieldsRow{
		{ID: complete, ExtraFields: []byte(`{"school":"KPI","motto":"go"}`)}, {ID: gap, ExtraFields: []byte(`{"school":"KPI"}`)},
	}, nil)
	q.EXPECT().SetEventTeamsFieldsMissing(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.SetEventTeamsFieldsMissingParams) error {
		got := map[uuid.UUID]int32{}
		for i, id := range arg.TeamIds {
			got[id] = arg.Missing[i]
		}
		if got[complete] != 0 || got[gap] != 1 {
			t.Fatalf("team missing counts = %v", got)
		}
		return nil
	})
	q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(2), nil)
	view, err := uc.ConfigureTeamFields(context.Background(), eventID, event.ConfigureParticipantFormInput{Enabled: true, Required: true, Document: teamPolicyDocument})
	if err != nil {
		t.Fatal(err)
	}
	if !view.RequireExisting || view.Answered != 2 {
		t.Fatalf("view = %+v", view)
	}
}

// «Not filled» is a standard table column: it filters without loading answers
// and its number reaches the row.
func TestParticipantsTableFiltersByMissingFieldsAndCarriesTheNumber(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventParticipantsTable(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventParticipantsTableParams) ([]postgres.ListEventParticipantsTableRow, error) {
			if arg.WithAnswers || string(arg.Filters) != `[{"key":"@missing","op":"bool","value":true}]` {
				t.Fatalf("params: %+v", arg)
			}
			return []postgres.ListEventParticipantsTableRow{{EventID: eventID, UserID: userID, Status: 2, CreatedAt: time.Now(), FieldsMissing: 2}}, nil
		})
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CountEventParticipantsTable(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().CountEventParticipantKinds(gomock.Any(), gomock.Any()).Return(postgres.CountEventParticipantKindsRow{}, nil)
	res, err := uc.ListParticipantsTable(context.Background(), event.ParticipantsTableQuery{
		EventID: eventID, Kind: "participants", Page: 1, PageSize: 25,
		Filters: []event.AnswerFilter{{Key: "@missing", Op: event.AnswerFilterBool, Value: true}},
	})
	if err != nil || len(res.Participants) != 1 || res.Participants[0].FieldsMissing != 2 {
		t.Fatalf("result = %+v err = %v", res, err)
	}
}

// The moderator's team form saves the ordinary answers; the staff-only values
// recorded through their own endpoint are kept, whatever the form sends.
func TestUpdateManagedTeamKeepsStaffOnlyValues(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	row := postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: captainID, MemberCount: 1, CreatedAt: now, UpdatedAt: now}
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	config := teamConfigRow(t, eventID, false, false)
	config.Required = false
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(config, nil).AnyTimes()
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{"school":"KPI","note":"VIP team"}`), nil).AnyTimes()
	q.EXPECT().UpdateEventTeamExtraFields(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamExtraFieldsParams) (int64, error) {
		var saved map[string]any
		_ = json.Unmarshal(arg.ExtraFields, &saved)
		if saved["school"] != "LNU" || saved["motto"] != "go" || saved["note"] != "VIP team" {
			t.Fatalf("saved = %v: the staff note must survive the team form", saved)
		}
		return 1, nil
	})
	q.EXPECT().RequestEventLabAccessSync(gomock.Any(), gomock.Any()).Return(postgres.EventLabAccessSync{EventTeamID: teamID}, nil).AnyTimes()

	_, err := uc.UpdateManagedTeam(context.Background(), eventID, teamID, event.UpdateManagedTeamInput{
		Name: "Blue", Fields: map[string]any{"school": "LNU", "motto": "go", "note": "overwritten by the form"},
	})
	if err != nil {
		t.Fatalf("UpdateManagedTeam: %v", err)
	}
}
