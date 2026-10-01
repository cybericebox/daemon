package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestParticipantsTableFiltersAndSortsColumns drives the participants table
// document: column filters (status, date range, name, pseudonym, team),
// column and answer sorting, paging and the total.
func TestParticipantsTableFiltersAndSortsColumns(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "ptable")
	var users []uuid.UUID
	for i, email := range []string{"ptable-c@test.test", "ptable-a@test.test", "ptable-b@test.test"} {
		user := mustSeedUser(t, db, email)
		users = append(users, user)
		if _, _, err := repo.Upsert(ctx, mustNewPendingParticipant(t, event.ID, user, epNow.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE event_participants SET status = 2 WHERE event_id = $1 AND user_id = $2`, event.ID, users[2]); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE event_participants SET pseudonym = 'Нічний Лис' WHERE event_id = $1 AND user_id = $2`, event.ID, users[1]); err != nil {
		t.Fatalf("pseudonym: %v", err)
	}
	emails := func(rows []participantRepo.Listed) string {
		out := ""
		for _, row := range rows {
			out += row.Email[len("ptable-") : len("ptable-")+1]
		}
		return out
	}
	for _, tc := range []struct {
		name, filters, sortKey string
		desc                   bool
		want                   string
	}{
		{"default newest first", `[]`, "@date", true, "bac"},
		{"sort by email", `[]`, "@email", false, "abc"},
		{"status any", `[{"key":"@status","op":"any","values":["1"]}]`, "@email", false, "ac"},
		{"date range", fmt.Sprintf(`[{"key":"@date","op":"range","type":"date","from":%q}]`, epNow.Add(30*time.Minute).UTC().Format(time.RFC3339)), "@email", false, "ab"},
		{"pseudonym contains", `[{"key":"@pseudonym","op":"contains","value":"лис"}]`, "@email", false, "a"},
		{"no team", `[{"key":"@team","op":"any","values":["none"]}]`, "@email", true, "cba"},
		{"email contains", `[{"key":"@email","op":"contains","value":"PTABLE-B"}]`, "@email", false, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := participantRepo.TableQuery{EventID: event.ID, StatusFilter: -1, Kind: participantRepo.KindAll, Filters: []byte(tc.filters), SortKey: tc.sortKey, SortDesc: tc.desc, Limit: 10}
			rows, err := repo.ListTable(ctx, query)
			if err != nil {
				t.Fatalf("ListTable: %v", err)
			}
			if got := emails(rows); got != tc.want {
				t.Fatalf("rows = %s, want %s", got, tc.want)
			}
			count, err := repo.CountTable(ctx, query)
			if err != nil || count != int64(len(tc.want)) {
				t.Fatalf("CountTable = %d err=%v, want %d", count, err, len(tc.want))
			}
		})
	}
	page, err := repo.ListTable(ctx, participantRepo.TableQuery{EventID: event.ID, StatusFilter: -1, Kind: participantRepo.KindAll, SortKey: "@email", Limit: 2, Offset: 2})
	if err != nil || emails(page) != "c" {
		t.Fatalf("second page = %s err=%v, want c", emails(page), err)
	}
}

// TestTeamsTableFiltersAndSortsColumns drives the teams table document:
// members range, status, pending invitees, captain and answer sorting.
func TestTeamsTableFiltersAndSortsColumns(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	teams := eventTeamRepo.New(db.Queries)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	owner, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "ttable-owner@example.test", now))
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	event, err := eventModel.NewEvent("teamtable", "Team Table", now, now.Add(24*time.Hour), owner.ID, now)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	createdEvent, err := eventRepo.New(db.Queries).Create(ctx, event)
	if err != nil {
		t.Fatalf("Create event: %v", err)
	}
	ids := map[string]uuid.UUID{}
	for i, row := range []struct {
		name, captain string
		members       int
		city, born    string
		slot          string
	}{
		{"Alpha", "Zoya", 3, "Одеса", "2026-01-10", "09:15"},
		{"Beta", "Andrii", 1, "Київ", "2026-03-05T10:00:00.000Z", "18:40"},
		{"Gamma", "Mariia", 2, "Львів", "не дата", ""},
	} {
		captain, cErr := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), fmt.Sprintf("ttable-%d@example.test", i), now))
		if cErr != nil {
			t.Fatalf("seed captain: %v", cErr)
		}
		if _, cErr = db.Pool.Exec(ctx, `UPDATE users SET first_name = $2 WHERE id = $1`, captain.ID, row.captain); cErr != nil {
			t.Fatalf("name captain: %v", cErr)
		}
		team, tErr := eventTeamModel.New(createdEvent.ID, captain.ID, row.name, fmt.Sprintf("ttable-code-%d", i), now.Add(time.Duration(i)*time.Minute))
		if tErr != nil {
			t.Fatalf("New team: %v", tErr)
		}
		if _, tErr = teams.Create(ctx, team); tErr != nil {
			t.Fatalf("Create team: %v", tErr)
		}
		if _, tErr = db.Pool.Exec(ctx, `UPDATE event_teams SET member_count = $2, extra_fields = jsonb_build_object('city', $3::text, 'born', $4::text, 'slot', $5::text) WHERE id = $1`, team.ID, row.members, row.city, row.born, row.slot); tErr != nil {
			t.Fatalf("seed team: %v", tErr)
		}
		ids[row.name] = team.ID
	}
	invitee, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "ttable-invitee@example.test", now))
	if err != nil {
		t.Fatalf("seed invitee: %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO event_participants (event_id, user_id, status, created_at, invited, invited_team_id, invited_to_team) VALUES ($1, $2, 1, $3, true, $4, true)`, createdEvent.ID, invitee.ID, now, ids["Gamma"]); err != nil {
		t.Fatalf("seed invitation: %v", err)
	}
	names := func(rows []eventTeamRepo.ListedTeam) string {
		out := ""
		for _, row := range rows {
			out += row.Team.Name[:1]
		}
		return out
	}
	for _, tc := range []struct {
		name, filters, sortKey string
		desc                   bool
		want                   string
	}{
		{"members desc", `[]`, "@members", true, "AGB"},
		{"captain asc", `[]`, "@captain", false, "BGA"},
		{"answer sort", `[]`, "city", false, "BGA"},
		{"members range", `[{"key":"@members","op":"range","type":"number","from":2}]`, "@name", false, "AG"},
		{"pending invitees", `[{"key":"@pending","op":"bool","value":true}]`, "@name", false, "G"},
		{"no pending", `[{"key":"@pending","op":"bool","value":false}]`, "@name", false, "AB"},
		{"captain contains", `[{"key":"@captain","op":"contains","value":"zoya"}]`, "@name", false, "A"},
		{"status any", `[{"key":"@status","op":"any","values":["admitted","manual","notAdmitted"]}]`, "@name", false, "ABG"},
		{"members equal", `[{"key":"@members","op":"range","type":"number","from":2,"to":2}]`, "@name", false, "G"},
		{"members greater", `[{"key":"@members","op":"range","type":"number","from":1,"fromExclusive":true}]`, "@name", false, "AG"},
		{"members less", `[{"key":"@members","op":"range","type":"number","to":3,"toExclusive":true}]`, "@name", false, "BG"},
		{"date answers after", `[{"key":"born","op":"range","type":"date","from":"2026-02-01T00:00:00Z"}]`, "@name", false, "B"},
		{"date answers before", `[{"key":"born","op":"range","type":"date","to":"2026-02-01T00:00:00Z"}]`, "@name", false, "A"},
		{"text not empty", `[{"key":"city","op":"present","value":true}]`, "@name", false, "ABG"},
		{"text empty", `[{"key":"city","op":"present","value":false}]`, "@name", false, ""},
		{"date answers sort", `[]`, "born", false, "ABG"},
		{"date-only bounds", `[{"key":"born","op":"range","type":"date","from":"2026-01-10","to":"2026-01-10"}]`, "@name", false, "A"},
		{"time of day range", `[{"key":"slot","op":"range","type":"time","from":"09:00","to":"12:00"}]`, "@name", false, "A"},
		{"time of day after", `[{"key":"slot","op":"range","type":"time","from":"12:00"}]`, "@name", false, "B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := eventTeamRepo.TableQuery{EventID: createdEvent.ID, Filters: []byte(tc.filters), SortKey: tc.sortKey, SortDesc: tc.desc, Limit: 10}
			rows, lErr := teams.ListTable(ctx, query)
			if lErr != nil {
				t.Fatalf("ListTable: %v", lErr)
			}
			if got := names(rows); got != tc.want {
				t.Fatalf("teams = %s, want %s", got, tc.want)
			}
			count, cErr := teams.CountTable(ctx, query)
			if cErr != nil || count != int64(len(tc.want)) {
				t.Fatalf("CountTable = %d err=%v, want %d", count, cErr, len(tc.want))
			}
		})
	}
}
