package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func imSeedTeam(t *testing.T, db *testhelpers.TestDB, event eventModel.Event, captainID uuid.UUID, name, code string) eventTeamModel.EventTeam {
	t.Helper()
	team, err := eventTeamModel.New(event.ID, captainID, name, code, ecNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(context.Background(), team); err != nil {
		t.Fatal(err)
	}
	return team
}

// imMakeActive puts the event inside its running window.
func imMakeActive(t *testing.T, db *testhelpers.TestDB, eventID uuid.UUID, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(),
		"UPDATE events SET archive_at = NULL, lifecycle_configured = true, available_from = $1, publish_at = $1, start_at = $2, finish_at = $3, withdraw_at = $4 WHERE id = $5",
		now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(time.Hour), now.Add(2*time.Hour), eventID); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentMonitoringListsActiveEventsByDefaultAndRecentOnRequest(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	repo := eventLabObservationRepo.New(db.Queries)

	active, activeCaptain := mustSeedEventForConfig(t, db, "imactive")
	imMakeActive(t, db, active.ID, now)
	idle, idleCaptain := mustSeedEventForConfig(t, db, "imidle")
	teamActive := imSeedTeam(t, db, active, activeCaptain, "Red Team", "im-active-team-code")
	teamIdle := imSeedTeam(t, db, idle, idleCaptain, "Blue Team", "im-idle-team-code")

	for _, row := range []struct {
		event, team uuid.UUID
		group       string
		updated     time.Time
	}{
		{active.ID, teamActive.ID, "g-active", now},
		{idle.ID, teamIdle.ID, "g-idle", now.Add(-time.Hour)},
	} {
		if err := db.Queries.UpsertLabMonitoringCurrent(ctx, postgres.UpsertLabMonitoringCurrentParams{
			EventID: row.event, EventTeamID: row.team, LabGroupName: row.group, AgentID: "agent-a", Sequence: 1,
			ObservedAt: row.updated, UpdatedAt: row.updated, Payload: []byte(`{"labs":[{"name":"web"}]}`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := repo.CurrentPlatform(ctx, false, now.Add(-labMonitoringModel.RecentWindow), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EventName != active.Name || got[0].TeamName != "Red Team" || got[0].LabGroupName != "g-active" {
		t.Fatalf("default = %+v, want only the active event with names", got)
	}

	got, err = repo.CurrentPlatform(ctx, true, now.Add(-labMonitoringModel.RecentWindow), now)
	if err != nil || len(got) != 2 {
		t.Fatalf("includeRecent = %+v err=%v, want both events", got, err)
	}
	got, err = repo.CurrentPlatform(ctx, true, now.Add(-30*time.Minute), now)
	if err != nil || len(got) != 1 {
		t.Fatalf("recent window must exclude stale idle rows: %+v err=%v", got, err)
	}

	// A snapshot prunes what it no longer reports for the agent.
	if err = repo.PruneCurrent(ctx, "agent-a", now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = repo.CurrentPlatform(ctx, true, now.Add(-labMonitoringModel.RecentWindow), now)
	if err != nil || len(got) != 1 || got[0].LabGroupName != "g-active" {
		t.Fatalf("after prune = %+v err=%v, want only the fresh row", got, err)
	}
}

func TestPlatformObservationsCarryNamesAndKeysetPaging(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	repo := eventLabObservationRepo.New(db.Queries)
	event, captain := mustSeedEventForConfig(t, db, "imobs")
	team := imSeedTeam(t, db, event, captain, "Green Team", "im-obs-team-code")

	ids := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		id := uuid.Must(uuid.NewV7())
		ids = append(ids, id)
		if _, err := repo.Append(ctx, labMonitoringModel.Observation{
			ID: id, EventID: event.ID, EventTeamID: team.ID, LabGroupName: "g", AgentID: "agent-a", Sequence: int64(i + 1),
			ObservedAt: now.Add(time.Duration(i) * time.Minute), ReceivedAt: now, SchemaVersion: 1, Snapshot: i == 0, Payload: []byte(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	cursorAt := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	maxID := uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
	page, err := repo.ListPlatform(ctx, uuid.NullUUID{}, uuid.NullUUID{}, now.Add(-time.Hour), now.Add(time.Hour), cursorAt, maxID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] || page[0].EventName != event.Name || page[0].TeamName != "Green Team" {
		t.Fatalf("first page = %+v, want newest two with names", page)
	}
	next, err := repo.ListPlatform(ctx, uuid.NullUUID{}, uuid.NullUUID{}, now.Add(-time.Hour), now.Add(time.Hour), cursorAt, page[1].ID, 2)
	if err != nil || len(next) != 1 || next[0].ID != ids[0] {
		t.Fatalf("second page = %+v err=%v, want the oldest row", next, err)
	}
}

func TestPlatformStandsFilterSearchAndPage(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	repo := platformStandRepo.New(db.Queries)

	alpha, alphaCaptain := mustSeedEventForConfig(t, db, "imalpha")
	beta, betaCaptain := mustSeedEventForConfig(t, db, "imbeta")
	seed := func(event eventModel.Event, captain uuid.UUID, name, code string, status eventStandModel.Status, reason string, at time.Time) eventTeamModel.EventTeam {
		team := imSeedTeam(t, db, event, captain, name, code)
		if _, err := db.Queries.CreateEventTeamStand(ctx, postgres.CreateEventTeamStandParams{
			EventTeamID: team.ID, EventID: event.ID, Status: int16(status), Reason: pgtype.Text{String: reason, Valid: reason != ""}, Generation: 2, Now: at,
		}); err != nil {
			t.Fatal(err)
		}
		return team
	}
	red := seed(alpha, alphaCaptain, "Red Team", "im-red-team-code", eventStandModel.StatusFailed, "ErrImagePull", now)
	seed(alpha, alphaCaptain, "Amber Team", "im-amber-team-code", eventStandModel.StatusReady, "", now.Add(-time.Minute))
	seed(beta, betaCaptain, "Blue Team", "im-blue-team-code", eventStandModel.StatusCreating, "", now.Add(-2*time.Minute))

	all, total, err := repo.List(ctx, platformStandRepo.Filter{Limit: 10})
	if err != nil || total != 3 || len(all) != 3 || all[0].TeamID != red.ID {
		t.Fatalf("all = %+v total=%d err=%v, want 3 newest first", all, total, err)
	}
	if all[0].EventName != alpha.Name || all[0].EventTag != "imalpha" || all[0].Reason != "ErrImagePull" || all[0].Generation != 2 {
		t.Fatalf("row = %+v", all[0])
	}

	failed, total, err := repo.List(ctx, platformStandRepo.Filter{Statuses: []eventStandModel.Status{eventStandModel.StatusFailed}, Limit: 10})
	if err != nil || total != 1 || len(failed) != 1 || failed[0].Status != eventStandModel.StatusFailed {
		t.Fatalf("failed = %+v total=%d err=%v", failed, total, err)
	}
	active, total, err := repo.List(ctx, platformStandRepo.Filter{Statuses: []eventStandModel.Status{eventStandModel.StatusCreating, eventStandModel.StatusReady}, Limit: 10})
	if err != nil || total != 2 || len(active) != 2 {
		t.Fatalf("active = %+v total=%d err=%v", active, total, err)
	}
	byEvent, total, err := repo.List(ctx, platformStandRepo.Filter{EventID: uuid.NullUUID{UUID: beta.ID, Valid: true}, Limit: 10})
	if err != nil || total != 1 || byEvent[0].TeamName != "Blue Team" {
		t.Fatalf("byEvent = %+v total=%d err=%v", byEvent, total, err)
	}
	for search, want := range map[string]int{"amber": 1, "IMBETA": 1, strings.ToLower(alpha.Name): 2, "nothing-matches": 0} {
		found, _, searchErr := repo.List(ctx, platformStandRepo.Filter{Search: search, Limit: 10})
		if searchErr != nil || len(found) != want {
			t.Fatalf("search %q -> %d rows err=%v, want %d", search, len(found), searchErr, want)
		}
	}
	page, total, err := repo.List(ctx, platformStandRepo.Filter{Limit: 2, Offset: 2})
	if err != nil || total != 3 || len(page) != 1 {
		t.Fatalf("page 2 = %+v total=%d err=%v", page, total, err)
	}

	events, err := repo.Events(ctx)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %+v err=%v, want the two events with stands", events, err)
	}
	counts, err := repo.CountByStatus(ctx)
	if err != nil || counts[eventStandModel.StatusFailed] != 1 || counts[eventStandModel.StatusReady] != 1 || counts[eventStandModel.StatusCreating] != 1 {
		t.Fatalf("counts = %v err=%v", counts, err)
	}
}
