package postgres_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestEventTeamListAnswerFilters drives event_answer_matches through the
// team list: every operator, and several filters combined with AND.
func TestEventTeamListAnswerFilters(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	teams := eventTeamRepo.New(db.Queries)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	cursorAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	cursorID := uuid.FromStringOrNil("ffffffff-ffff-ffff-ffff-ffffffffffff")

	owner, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "filters-owner@example.test", now))
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	event, err := eventModel.NewEvent("teamfilters", "Team Filters", now, now.Add(24*time.Hour), owner.ID, now)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	createdEvent, err := eventRepo.New(db.Queries).Create(ctx, event)
	if err != nil {
		t.Fatalf("Create event: %v", err)
	}
	answers := map[string]map[string]any{
		"Alpha": {"city": "Київ", "langs": []any{"Go", "Rust"}, "size": "S", "agree": true, "cv": map[string]any{"id": uuid.Must(uuid.NewV7()).String(), "name": "cv.pdf"}},
		"Beta":  {"city": "Львів", "langs": []any{"Python"}, "size": "M", "agree": false, "cv": nil},
		"Gamma": {},
	}
	for i, name := range []string{"Alpha", "Beta", "Gamma"} {
		captain, cErr := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), fmt.Sprintf("filters-%d@example.test", i), now))
		if cErr != nil {
			t.Fatalf("seed captain: %v", cErr)
		}
		team, tErr := eventTeamModel.New(createdEvent.ID, captain.ID, name, fmt.Sprintf("filters-code-%d", i), now.Add(time.Duration(i)*time.Minute))
		if tErr != nil {
			t.Fatalf("New team: %v", tErr)
		}
		if _, tErr = teams.Create(ctx, team); tErr != nil {
			t.Fatalf("Create team: %v", tErr)
		}
		if _, tErr = teams.UpdateExtraFields(ctx, createdEvent.ID, team.ID, answers[name]); tErr != nil {
			t.Fatalf("UpdateExtraFields: %v", tErr)
		}
	}

	for _, tc := range []struct {
		name    string
		filters string
		want    []string
	}{
		{"no filters", `[]`, []string{"Alpha", "Beta", "Gamma"}},
		{"contains ignores case", `[{"key":"city","op":"contains","value":"київ"}]`, []string{"Alpha"}},
		{"any on multi choice", `[{"key":"langs","op":"any","values":["Rust","Java"]}]`, []string{"Alpha"}},
		{"any on single choice", `[{"key":"size","op":"any","values":["M","L"]}]`, []string{"Beta"}},
		{"checkbox yes", `[{"key":"agree","op":"bool","value":true}]`, []string{"Alpha"}},
		{"checkbox no counts missing", `[{"key":"agree","op":"bool","value":false}]`, []string{"Beta", "Gamma"}},
		{"file present", `[{"key":"cv","op":"present","value":true}]`, []string{"Alpha"}},
		{"file missing", `[{"key":"cv","op":"present","value":false}]`, []string{"Beta", "Gamma"}},
		{"all filters must match", `[{"key":"agree","op":"bool","value":false},{"key":"city","op":"contains","value":"львів"}]`, []string{"Beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, lErr := teams.List(ctx, createdEvent.ID, "", -1, []byte(tc.filters), cursorAt, cursorID, 10)
			if lErr != nil {
				t.Fatalf("List: %v", lErr)
			}
			got := make([]string, 0, len(rows))
			for _, row := range rows {
				got = append(got, row.Team.Name)
			}
			sort.Strings(got)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("teams = %v, want %v", got, tc.want)
			}
			count, cErr := teams.CountMatching(ctx, createdEvent.ID, "", -1, []byte(tc.filters))
			if cErr != nil || count != int64(len(tc.want)) {
				t.Fatalf("count = %d err=%v, want %d", count, cErr, len(tc.want))
			}
		})
	}
}

// TestParticipantListRegistrationAnswerFilters filters by the latest
// registration answers of each participant; older answers are ignored.
func TestParticipantListRegistrationAnswerFilters(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)

	event := mustSeedEventForParticipants(t, db, "pfilters")
	userA := mustSeedUser(t, db, "pfilters-a@test.test")
	userB := mustSeedUser(t, db, "pfilters-b@test.test")
	for i, user := range []uuid.UUID{userA, userB} {
		if _, _, err := repo.Upsert(ctx, mustNewPendingParticipant(t, event.ID, user, epNow.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	formID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := db.Queries.CreateInitialEventForm(ctx, postgres.CreateInitialEventFormParams{
		ID: versionID, FormID: formID, EventID: event.ID, Title: "Registration", Purpose: "registration",
		Version: 1, Enabled: true, Required: true, Document: []byte(`{"blocks":[]}`), CreatedAt: epNow,
	}); err != nil {
		t.Fatalf("create form: %v", err)
	}
	nextVersionID := uuid.Must(uuid.NewV7())
	if _, err := db.Queries.CreateEventFormVersion(ctx, postgres.CreateEventFormVersionParams{
		ID: nextVersionID, FormID: formID, EventID: event.ID, Version: 2,
		Enabled: true, Required: true, Document: []byte(`{"blocks":[]}`), CreatedAt: epNow.Add(time.Minute),
	}); err != nil {
		t.Fatalf("create second version: %v", err)
	}
	for _, row := range []struct {
		user    uuid.UUID
		version uuid.UUID
		answers string
		at      time.Time
	}{
		{userA, versionID, `{"city":"Київ"}`, epNow},
		{userB, versionID, `{"city":"Київ"}`, epNow},
		{userB, nextVersionID, `{"city":"Одеса"}`, epNow.Add(time.Hour)},
	} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at) VALUES ($1, $2, $3, $4::jsonb, $5)`, event.ID, row.user, row.version, row.answers, row.at); err != nil {
			t.Fatalf("seed answer: %v", err)
		}
	}
	filters := []byte(`[{"key":"city","op":"contains","value":"київ"}]`)
	rows, err := repo.List(ctx, event.ID, -1, participantRepo.KindAll, "", filters, epCursorSentinelTime, epMaxUUID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Participant.UserID != userA {
		t.Fatalf("List = %+v, want only userA", rows)
	}
	count, err := repo.CountMatching(ctx, event.ID, -1, participantRepo.KindAll, "", filters)
	if err != nil || count != 1 {
		t.Fatalf("CountMatching = %d err=%v, want 1", count, err)
	}
	all, err := repo.CountMatching(ctx, event.ID, -1, participantRepo.KindAll, "", nil)
	if err != nil || all != 2 {
		t.Fatalf("CountMatching without filters = %d err=%v, want 2", all, err)
	}
}
