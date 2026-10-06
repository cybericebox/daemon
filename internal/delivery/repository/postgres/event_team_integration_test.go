package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventTeamCreateAndEventScopedLookup(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)
	teams := eventTeamRepo.New(db.Queries)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	captain, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "captain@example.test", now))
	if err != nil {
		t.Fatalf("seed captain: %v", err)
	}
	event, err := eventModel.NewEvent("teamscope", "Team Scope", now, now.Add(24*time.Hour), captain.ID, now)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	createdEvent, err := events.Create(ctx, event)
	if err != nil {
		t.Fatalf("Create event: %v", err)
	}
	team, err := eventTeamModel.New(createdEvent.ID, captain.ID, "Blue Team", "secure-join-code", now)
	if err != nil {
		t.Fatalf("New team: %v", err)
	}
	created, err := teams.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create team: %v", err)
	}
	if created.ID != team.ID || created.CaptainID != captain.ID || created.MemberCount != 1 {
		t.Fatalf("created team mismatch: %+v", created)
	}
	byCode, err := teams.GetByJoinCode(ctx, createdEvent.ID, "secure-join-code")
	if err != nil || byCode.ID != created.ID {
		t.Fatalf("GetByJoinCode: team=%+v err=%v", byCode, err)
	}
	if _, err = teams.GetByID(ctx, uuid.Must(uuid.NewV7()), created.ID); err != pgx.ErrNoRows {
		t.Fatalf("lookup under another event must be hidden, got %v", err)
	}
}

// TestEventTeamListSearchAndAdmissionFilters drives the management list SQL:
// the name search is case-insensitive and the admission filter splits the
// list by the same rule that fills each row's Admitted flag.
func TestEventTeamListSearchAndAdmissionFilters(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)
	teams := eventTeamRepo.New(db.Queries)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	cursorAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	cursorID := uuid.FromStringOrNil("ffffffff-ffff-ffff-ffff-ffffffffffff")

	owner, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "owner@example.test", now))
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	event, err := eventModel.NewEvent("teamsearch", "Team Search", now, now.Add(24*time.Hour), owner.ID, now)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	createdEvent, err := events.Create(ctx, event)
	if err != nil {
		t.Fatalf("Create event: %v", err)
	}
	for i, name := range []string{"Blue Team", "Red Squad"} {
		captain, cErr := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), name[:3]+"@example.test", now))
		if cErr != nil {
			t.Fatalf("seed captain: %v", cErr)
		}
		team, tErr := eventTeamModel.New(createdEvent.ID, captain.ID, name, "join-code-"+name[:3], now.Add(time.Duration(i)*time.Minute))
		if tErr != nil {
			t.Fatalf("New team: %v", tErr)
		}
		if _, tErr = teams.Create(ctx, team); tErr != nil {
			t.Fatalf("Create team: %v", tErr)
		}
	}

	found, err := teams.List(ctx, createdEvent.ID, "bLuE", -1, nil, cursorAt, cursorID, 10)
	if err != nil || len(found) != 1 || found[0].Team.Name != "Blue Team" {
		t.Fatalf("search list = %+v err=%v, want only Blue Team", found, err)
	}
	if count, cErr := teams.CountMatching(ctx, createdEvent.ID, "bLuE", -1, nil); cErr != nil || count != 1 {
		t.Fatalf("search count = %d err=%v, want 1", count, cErr)
	}

	all, err := teams.List(ctx, createdEvent.ID, "", -1, nil, cursorAt, cursorID, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list = %+v err=%v, want 2 teams", all, err)
	}
	var total int64
	for _, admission := range []int32{0, 1} {
		rows, lErr := teams.List(ctx, createdEvent.ID, "", admission, nil, cursorAt, cursorID, 10)
		if lErr != nil {
			t.Fatalf("admission %d list: %v", admission, lErr)
		}
		for _, row := range rows {
			if row.Admitted != (admission == 1) {
				t.Fatalf("admission %d returned team %q with Admitted=%v", admission, row.Team.Name, row.Admitted)
			}
		}
		count, cErr := teams.CountMatching(ctx, createdEvent.ID, "", admission, nil)
		if cErr != nil || count != int64(len(rows)) {
			t.Fatalf("admission %d count = %d err=%v, want %d", admission, count, cErr, len(rows))
		}
		total += count
	}
	if total != 2 {
		t.Fatalf("admitted + not admitted = %d, want 2", total)
	}
}
