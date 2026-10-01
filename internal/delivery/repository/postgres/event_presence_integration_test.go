package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// A participant's event presence is touched at most once a minute, the last
// lab access is the later of the lab counters and the VPN sessions, and both
// show up in the manage list, the team members, the detail and the usage view;
// the team's last sign of life counts the lab access too.
func TestParticipantPresenceAndLastLabAccess(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)
	f := anSeed(t, db, "presence")

	// Never online, never in a lab.
	acts, err := repo.Activities(ctx, f.event, []uuid.UUID{f.user})
	if err != nil || acts[f.user].LastSeenAt != nil || acts[f.user].LastLabAt != nil {
		t.Fatalf("before any activity: %+v err=%v", acts, err)
	}
	detail, err := repo.Detail(ctx, f.event, f.user)
	if err != nil || detail.LastSeenAt != nil || detail.LastLabAt != nil {
		t.Fatalf("detail before: %+v err=%v", detail, err)
	}

	// Presence: a touch within a minute of the stored time is ignored.
	if err = repo.TouchPresence(ctx, f.event, f.user, anStart); err != nil {
		t.Fatal(err)
	}
	if err = repo.TouchPresence(ctx, f.event, f.user, anStart.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	acts, _ = repo.Activities(ctx, f.event, []uuid.UUID{f.user})
	if got := acts[f.user].LastSeenAt; got == nil || !got.Equal(anStart) {
		t.Fatalf("a touch inside a minute is ignored: %v", got)
	}
	if err = repo.TouchPresence(ctx, f.event, f.user, anStart.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	acts, _ = repo.Activities(ctx, f.event, []uuid.UUID{f.user})
	if got := acts[f.user].LastSeenAt; got == nil || !got.Equal(anStart.Add(5*time.Minute)) {
		t.Fatalf("a later touch is stored: %v", got)
	}
	// Presence is per event.
	other := mustSeedEventForParticipants(t, db, "presenceother")
	if acts, _ = repo.Activities(ctx, other.ID, []uuid.UUID{f.user}); acts[f.user].LastSeenAt != nil {
		t.Fatalf("presence leaked into another event: %+v", acts)
	}

	// Lab access: the proxy counter, then a later VPN session.
	rtExec(t, db, `INSERT INTO event_lab_touches (id, event_id, team_id, user_id, event_challenge_id, surface, attempts_count, first_seen_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, 'proxy', 1, $6, $7)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.user, f.challenge, anStart, anStart.Add(10*time.Minute))
	acts, _ = repo.Activities(ctx, f.event, []uuid.UUID{f.user})
	if got := acts[f.user].LastLabAt; got == nil || !got.Equal(anStart.Add(10*time.Minute)) {
		t.Fatalf("proxy lab access: %v", got)
	}
	rtExec(t, db, `INSERT INTO event_vpn_sessions (id, event_id, team_id, user_id, client_name, started_at, ended_at, rx_min, rx_max, tx_min, tx_max)
VALUES ($1, $2, $3, $4, 'c', $5, $6, 0, 1, 0, 1)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.user, anStart.Add(20*time.Minute), anStart.Add(25*time.Minute))
	acts, _ = repo.Activities(ctx, f.event, []uuid.UUID{f.user})
	if got := acts[f.user].LastLabAt; got == nil || !got.Equal(anStart.Add(25*time.Minute)) {
		t.Fatalf("the VPN session is later: %v", got)
	}

	// The manage list, the team members and the detail carry both times.
	rows, err := repo.ListTable(ctx, participantRepo.TableQuery{EventID: f.event, StatusFilter: -1, SortKey: "@lastLab", SortDesc: true, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("table: %+v err=%v", rows, err)
	}
	if rows[0].LastSeenAt == nil || !rows[0].LastSeenAt.Equal(anStart.Add(5*time.Minute)) || rows[0].LastLabAt == nil || !rows[0].LastLabAt.Equal(anStart.Add(25*time.Minute)) {
		t.Fatalf("table row: %+v", rows[0])
	}
	if _, err = repo.ListTable(ctx, participantRepo.TableQuery{EventID: f.event, StatusFilter: -1, SortKey: "@lastSeen", Limit: 10}); err != nil {
		t.Fatalf("sort by last seen: %v", err)
	}
	members, err := repo.TeamMembers(ctx, f.event, []uuid.UUID{f.team})
	if err != nil || len(members) != 1 || members[0].LastSeenAt == nil || members[0].LastLabAt == nil || !members[0].LastLabAt.Equal(anStart.Add(25*time.Minute)) {
		t.Fatalf("team members: %+v err=%v", members, err)
	}
	if detail, err = repo.Detail(ctx, f.event, f.user); err != nil || detail.LastSeenAt == nil || detail.LastLabAt == nil {
		t.Fatalf("detail: %+v err=%v", detail, err)
	}

	// The usage view of the same participant.
	usage, err := eventAnalyticsRepo.New(db.Queries).UsageUsers(ctx, f.event, nil)
	if err != nil || len(usage) != 1 || usage[0].LastSeenAt == nil || usage[0].LastLabAt == nil || !usage[0].LastLabAt.Equal(anStart.Add(25*time.Minute)) {
		t.Fatalf("usage users: %+v err=%v", usage, err)
	}

	// The team is not inactive while a member is in a lab.
	activity, err := eventAnalyticsRepo.New(db.Queries).TeamActivity(ctx, f.event, anStart.Add(time.Hour))
	if err != nil || len(activity) == 0 {
		t.Fatalf("team activity: %+v err=%v", activity, err)
	}
	for _, a := range activity {
		if a.TeamID == f.team && (a.LastActivityAt == nil || !a.LastActivityAt.Equal(anStart.Add(25*time.Minute))) {
			t.Fatalf("lab access counts as team activity: %v", a.LastActivityAt)
		}
	}
}
