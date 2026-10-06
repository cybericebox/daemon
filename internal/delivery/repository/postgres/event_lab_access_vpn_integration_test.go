package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestTeamGroupCleanupIsQueuedWithoutChallengeLabs(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, captainID := mustSeedEventForConfig(t, db, "emptygroupcleanup")
	team, err := eventTeamModel.New(event.ID, captainID, "Early Team", "early-cleanup-code", ecNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	group, err := labBindingModel.GroupName(event.ID, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := db.Queries.QueueTeamLabGroupCleanup(ctx, postgres.QueueTeamLabGroupCleanupParams{EventTeamID: team.ID, RequestedAt: ecNow})
	if err != nil || affected != 1 {
		t.Fatalf("queue empty group: affected=%d err=%v", affected, err)
	}
	groups, err := db.Queries.ListPendingLabGroupCleanupRequests(ctx)
	if err != nil || len(groups) != 1 || groups[0] != group {
		t.Fatalf("queued groups=%v err=%v want %s", groups, err, group)
	}
}

func TestWithdrawnTeamWithoutChallengeLabsQueuesGroup(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, captainID := mustSeedEventForConfig(t, db, "withdrawnemptygroup")
	team, err := eventTeamModel.New(event.ID, captainID, "Early Team", "withdrawn-early-code", ecNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, "UPDATE events SET publish_at = $1, start_at = $2, finish_at = $3, withdraw_at = $4 WHERE id = $5", ecNow, ecNow.Add(time.Hour), ecNow.Add(2*time.Hour), ecNow.Add(3*time.Hour), event.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.Queries.QueueWithdrawnEmptyLabGroups(ctx, ecNow.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	group, err := labBindingModel.GroupName(event.ID, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := db.Queries.ListPendingLabGroupCleanupRequests(ctx)
	if err != nil || len(groups) != 1 || groups[0] != group {
		t.Fatalf("queued groups=%v err=%v want %s", groups, err, group)
	}
}

func TestEventLabAccessSyncDetectsVPNSettingChanges(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, captainID := mustSeedEventForConfig(t, db, "vpnaccesssync")
	if _, err := eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(event.ID, ecNow)); err != nil {
		t.Fatal(err)
	}
	team, err := eventTeamModel.New(event.ID, captainID, "VPN Team", "vpn-access-code", ecNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	syncs := labAccessSyncRepo.New(db.Queries)
	if err = syncs.Request(ctx, team.ID, ecNow); err != nil {
		t.Fatal(err)
	}
	assertState := func(want bool) {
		t.Helper()
		rows, err := syncs.ListDirty(ctx, 10)
		if err != nil || len(rows) != 1 || rows[0].TeamID != team.ID || rows[0].VPNEnabled != want {
			t.Fatalf("dirty rows=%+v err=%v want VPN=%t", rows, err, want)
		}
		if _, err := syncs.MarkApplied(ctx, team.ID, rows[0].DesiredRevision, rows[0].RuntimeOpen, rows[0].VPNEnabled, rows[0].StageEpoch, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	assertState(false)
	for _, enabled := range []bool{true, false} {
		if _, err := db.Pool.Exec(ctx, "UPDATE events SET infrastructure_allowed = $1 WHERE id = $2", enabled, event.ID); err != nil {
			t.Fatal(err)
		}
		assertState(enabled)
	}
	rows, err := syncs.ListDirty(ctx, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("unexpected pending sync rows=%+v err=%v", rows, err)
	}
}

// TestModeratorsTeamIsHiddenAndItsVPNClientsAreManagers covers the W5 SQL:
// the hidden moderators team is invisible to team reads, its VPN clients are
// the event owner and moderators (never observers), and the final teardown
// disables every team VPN group.
func TestModeratorsTeamIsHiddenAndItsVPNClientsAreManagers(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, ownerID := mustSeedEventForConfig(t, db, "moderatorsteam")
	moderatorID := mustSeedUser(t, db, "moderators-moderator@test.test")
	observerID := mustSeedUser(t, db, "moderators-observer@test.test")
	for _, m := range []struct {
		user uuid.UUID
		role eventManagerModel.Role
	}{{ownerID, eventManagerModel.RoleOwner}, {moderatorID, eventManagerModel.RoleManager}, {observerID, eventManagerModel.RoleViewer}} {
		membership, err := eventManagerModel.New(event.ID, m.user, m.role, ecNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = eventManagerRepo.New(db.Queries).Create(ctx, membership); err != nil {
			t.Fatal(err)
		}
	}
	stands := eventStandRepo.New(db.Queries)
	for range 2 {
		if err := stands.EnsureModeratorsTeam(ctx, event.ID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV4()).String(), ecNow); err != nil {
			t.Fatal(err)
		}
	}
	team, err := stands.GetModeratorsTeam(ctx, event.ID)
	if err != nil || !team.Hidden || !team.Moderators || team.CaptainID != ownerID {
		t.Fatalf("moderators team = %+v, %v", team, err)
	}
	teams := eventTeamRepo.New(db.Queries)
	if _, err = teams.GetByID(ctx, event.ID, team.ID); err == nil {
		t.Fatal("team routes must not see the moderators team")
	}
	if count, err := db.Queries.CountEventTeams(ctx, event.ID); err != nil || count != 0 {
		t.Fatalf("moderators team counted against MaxTeams: %d, %v", count, err)
	}
	clients, err := labAccessSyncRepo.New(db.Queries).Clients(ctx, team.ID)
	if err != nil || len(clients) != 2 {
		t.Fatalf("moderators VPN clients = %v, %v; want owner and moderator only", clients, err)
	}
	for _, client := range clients {
		if client == observerID {
			t.Fatal("observers must not get a moderators VPN client")
		}
	}
	syncs := labAccessSyncRepo.New(db.Queries)
	// Without infrastructure the moderators team (board only) never syncs.
	if err = syncs.RequestModerators(ctx, event.ID, ecNow); err != nil {
		t.Fatal(err)
	}
	if err = syncs.RequestEvent(ctx, event.ID, ecNow); err != nil {
		t.Fatal(err)
	}
	if rows, dirtyErr := syncs.ListDirty(ctx, 10); dirtyErr != nil || len(rows) != 0 {
		t.Fatalf("moderators sync without infrastructure = %+v, %v", rows, dirtyErr)
	}
	if _, err = db.Pool.Exec(ctx, "UPDATE events SET infrastructure_allowed = true WHERE id = $1", event.ID); err != nil {
		t.Fatal(err)
	}
	if err = syncs.RequestModerators(ctx, event.ID, ecNow); err != nil {
		t.Fatal(err)
	}
	rows, err := syncs.ListDirty(ctx, 10)
	if err != nil || len(rows) != 1 || !rows[0].VPNEnabled {
		t.Fatalf("moderators sync = %+v, %v", rows, err)
	}
	if err = stands.TearDownRollout(ctx, event.ID, ecNow); err != nil {
		t.Fatal(err)
	}
	rows, err = syncs.ListDirty(ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].VPNEnabled {
		t.Fatalf("after teardown no team VPN group may be ensured again: %+v, %v", rows, err)
	}
}
