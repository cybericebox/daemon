package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func policyForm(requireExisting, block bool) eventFormModel.Form {
	return eventFormModel.Form{Version: 1, Enabled: true, Required: true, RequireExisting: requireExisting, BlockSubmissions: block, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City", Required: true},
		{ID: "note", Type: eventContentModel.BlockField, Key: "note", Input: "long_text", Label: "Note", StaffOnly: true},
	}}}
}

// The policy travels with the form version, and «missing» is a table column
// organizers can filter on (bool filter on "@missing").
func TestFieldPolicyIsStoredPerVersionAndFiltersParticipants(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "policy")
	forms := eventFormRepo.New(db.Queries)
	participants := participantRepo.New(db.Queries)

	first, err := forms.Create(ctx, eventFormRepo.Version{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Form: policyForm(false, false), CreatedAt: itNow})
	if err != nil || first.Form.RequireExisting || first.Form.BlockSubmissions {
		t.Fatalf("first version = %+v err=%v", first, err)
	}
	next := policyForm(true, true)
	next.Version = 2
	second, err := forms.Create(ctx, eventFormRepo.Version{ID: uuid.Must(uuid.NewV7()), FormID: first.FormID, EventID: event.ID, Form: next, CreatedAt: itNow.Add(time.Minute)})
	if err != nil || !second.Form.RequireExisting || !second.Form.BlockSubmissions {
		t.Fatalf("second version = %+v err=%v", second, err)
	}
	latest, err := forms.Latest(ctx, event.ID)
	if err != nil || latest.Form.Version != 2 || !latest.Form.RequireExisting || !latest.Form.BlockSubmissions {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}

	var users []uuid.UUID
	for i, email := range []string{"policy-a@test.test", "policy-b@test.test", "policy-c@test.test"} {
		user := mustSeedUser(t, db, email)
		users = append(users, user)
		if _, _, err = participants.Upsert(ctx, mustNewPendingParticipant(t, event.ID, user, epNow.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	if err = forms.SetFieldsMissing(ctx, event.ID, map[uuid.UUID]int32{users[0]: 0, users[1]: 2, users[2]: 1}); err != nil {
		t.Fatalf("SetFieldsMissing: %v", err)
	}
	if missing, missingErr := forms.FieldsMissing(ctx, event.ID, users[1]); missingErr != nil || missing != 2 {
		t.Fatalf("FieldsMissing = %d err=%v", missing, missingErr)
	}
	for _, tc := range []struct {
		name, filters string
		want          int
	}{
		{"who has not filled", `[{"key":"@missing","op":"bool","value":true}]`, 2},
		{"who is complete", `[{"key":"@missing","op":"bool","value":false}]`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := participantRepo.TableQuery{EventID: event.ID, StatusFilter: -1, Kind: participantRepo.KindAll, Filters: []byte(tc.filters), SortKey: "@email", Limit: 10}
			rows, listErr := participants.ListTable(ctx, query)
			if listErr != nil || len(rows) != tc.want {
				t.Fatalf("rows = %d err=%v, want %d", len(rows), listErr, tc.want)
			}
			if count, countErr := participants.CountTable(ctx, query); countErr != nil || count != int64(tc.want) {
				t.Fatalf("count = %d err=%v, want %d", count, countErr, tc.want)
			}
		})
	}
	rows, err := participants.ListTable(ctx, participantRepo.TableQuery{EventID: event.ID, StatusFilter: -1, Kind: participantRepo.KindAll, SortKey: "@email", Limit: 10})
	if err != nil || len(rows) != 3 || rows[1].FieldsMissing != 2 || rows[2].FieldsMissing != 1 {
		t.Fatalf("listed rows carry the number: %+v err=%v", rows, err)
	}
}

// Staff-only values live in the participant's answer row: editing them keeps
// the moment the participant answered, and the audit names who changed what.
func TestStaffFieldsStayInTheAnswerRowAndAreAudited(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "staffaudit")
	forms := eventFormRepo.New(db.Queries)
	user := mustSeedUser(t, db, "staffaudit-user@test.test")
	actor := mustSeedUser(t, db, "staffaudit-actor@test.test")
	version, err := forms.Create(ctx, eventFormRepo.Version{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Form: policyForm(false, false), CreatedAt: itNow})
	if err != nil {
		t.Fatal(err)
	}
	answeredAt := itNow.Add(time.Hour)
	if _, err = forms.SaveAnswer(ctx, eventFormRepo.Answer{EventID: event.ID, UserID: user, FormVersionID: version.ID, Values: map[string]any{"city": "Kyiv"}, SubmittedAt: answeredAt}); err != nil {
		t.Fatal(err)
	}
	row, err := forms.LatestAnswerRow(ctx, event.ID, user)
	if err != nil || row.FormVersionID != version.ID || row.Values["city"] != "Kyiv" {
		t.Fatalf("answer row = %+v err=%v", row, err)
	}
	if updated, updateErr := forms.UpdateAnswerValues(ctx, event.ID, user, version.ID, map[string]any{"city": "Kyiv", "note": "VIP"}); updateErr != nil || !updated {
		t.Fatalf("update values: %v %v", updated, updateErr)
	}
	answers, err := forms.ListAnswers(ctx, event.ID, version.FormID)
	if err != nil || len(answers) != 1 || answers[0].Values["note"] != "VIP" || !answers[0].SubmittedAt.Equal(answeredAt) {
		t.Fatalf("answers = %+v err=%v: submitted_at must not move on a staff edit", answers, err)
	}
	if count, countErr := forms.CountAnswered(ctx, event.ID); countErr != nil || count != 1 {
		t.Fatalf("answered = %d err=%v", count, countErr)
	}

	if _, found, changeErr := forms.LastStaffChange(ctx, event.ID, "participant", user); changeErr != nil || found {
		t.Fatalf("no change yet: found=%v err=%v", found, changeErr)
	}
	if err = forms.RecordStaffChange(ctx, event.ID, "participant", user, actor, []string{"note"}, itNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = forms.RecordStaffChange(ctx, event.ID, "participant", user, actor, []string{"came", "note"}, itNow.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	change, found, err := forms.LastStaffChange(ctx, event.ID, "participant", user)
	if err != nil || !found || len(change.Keys) != 2 || change.ActorID == nil || *change.ActorID != actor || !change.At.Equal(itNow.Add(3*time.Hour)) {
		t.Fatalf("last change = %+v found=%v err=%v", change, found, err)
	}
	if _, other, otherErr := forms.LastStaffChange(ctx, event.ID, "team", user); otherErr != nil || other {
		t.Fatalf("the audit must be scoped to participants or teams: %v %v", other, otherErr)
	}
}

func TestTeamFieldPolicyAndMissingColumn(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "teampolicy")
	teams := eventTeamRepo.New(db.Queries)
	form := policyForm(true, true)
	if _, err := teams.PutFieldConfig(ctx, event.ID, form, itNow); err != nil {
		t.Fatal(err)
	}
	stored, err := teams.GetFieldConfig(ctx, event.ID)
	if err != nil || !stored.RequireExisting || !stored.BlockSubmissions {
		t.Fatalf("team policy = %+v err=%v", stored, err)
	}
	var ids []uuid.UUID
	for i, name := range []string{"Alpha", "Beta"} {
		captain := mustSeedUser(t, db, "teampolicy-"+name+"@test.test")
		team, teamErr := eventTeamModel.New(event.ID, captain, name, "teampolicy-code-"+name, itNow.Add(time.Duration(i)*time.Minute))
		if teamErr != nil {
			t.Fatal(teamErr)
		}
		created, teamErr := teams.Create(ctx, team)
		if teamErr != nil {
			t.Fatal(teamErr)
		}
		ids = append(ids, created.ID)
	}
	if _, err = teams.SaveExtraFields(ctx, event.ID, ids[0], map[string]any{"city": "Kyiv"}, 0); err != nil {
		t.Fatal(err)
	}
	if err = teams.SetFieldsMissing(ctx, event.ID, map[uuid.UUID]int32{ids[1]: 1}); err != nil {
		t.Fatal(err)
	}
	if missing, missingErr := teams.FieldsMissing(ctx, event.ID, ids[1]); missingErr != nil || missing != 1 {
		t.Fatalf("team missing = %d err=%v", missing, missingErr)
	}
	// SaveExtraFields stores the count with the answers; the plain update leaves it.
	if _, err = teams.UpdateExtraFields(ctx, event.ID, ids[1], map[string]any{"note": "x"}); err != nil {
		t.Fatal(err)
	}
	if missing, missingErr := teams.FieldsMissing(ctx, event.ID, ids[1]); missingErr != nil || missing != 1 {
		t.Fatalf("a staff-only edit must not touch the number: %d err=%v", missing, missingErr)
	}
	query := eventTeamRepo.TableQuery{EventID: event.ID, Filters: []byte(`[{"key":"@missing","op":"bool","value":true}]`), SortKey: "@name", Limit: 10}
	rows, err := teams.ListTable(ctx, query)
	if err != nil || len(rows) != 1 || rows[0].Team.ID != ids[1] || rows[0].FieldsMissing != 1 {
		t.Fatalf("teams that have not filled = %+v err=%v", rows, err)
	}
	if count, countErr := teams.CountTable(ctx, query); countErr != nil || count != 1 {
		t.Fatalf("count = %d err=%v", count, countErr)
	}
	listed, err := teams.ListTeamFields(ctx, event.ID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("team fields = %+v err=%v", listed, err)
	}
}
