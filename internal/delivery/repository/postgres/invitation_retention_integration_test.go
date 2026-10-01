package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// rtScheduledEvent seeds an event that starts at startAt and finishes a week
// later; lockedAtStart selects join policy 0 (registration closes at start).
func rtScheduledEvent(t *testing.T, db *testhelpers.TestDB, tag string, startAt time.Time, lockedAtStart bool) uuid.UUID {
	t.Helper()
	event := mustSeedEventForParticipants(t, db, tag)
	policy := 1
	if lockedAtStart {
		policy = 0
	}
	rtExec(t, db, `
UPDATE events
SET lifecycle_configured = true, join_policy = $5, publish_at = $2, start_at = $2, finish_at = $3, withdraw_at = $4
WHERE id = $1`, event.ID, startAt, startAt.AddDate(0, 0, 7), startAt.AddDate(1, 0, 0), policy)
	return event.ID
}

// Dead invitations disappear after the grace period; live ones and accepted
// participations stay.
func TestRetentionPurgesExpiredInvitations(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	participants := participantRepo.New(db.Queries)
	cutoff := rtNow.AddDate(0, 0, -7)
	const batch = 2

	finished := rtScheduledEvent(t, db, "invfinished", cutoff.AddDate(0, 0, -30), false)
	rolling := rtScheduledEvent(t, db, "invrolling", cutoff.AddDate(0, 0, -2), false)
	upcoming := rtScheduledEvent(t, db, "invupcoming", rtNow.AddDate(0, 1, 0), true)

	manager := mustSeedUser(t, db, "invitation-manager@test.test")
	invite := func(eventID uuid.UUID, email string, team uuid.NullUUID) uuid.UUID {
		userID := mustSeedUser(t, db, email)
		if _, ok, err := participants.Invite(ctx, eventID, userID, manager, team, rtNow); err != nil || !ok {
			t.Fatalf("invite %s: ok=%v err=%v", email, ok, err)
		}
		return userID
	}
	invite(finished, "dead-finished@test.test", uuid.NullUUID{})
	liveRolling := invite(rolling, "live-rolling@test.test", uuid.NullUUID{})
	captain := mustSeedUser(t, db, "captain-pending@test.test")
	team, err := eventTeamModel.NewManaged(rolling, captain, "Pending Team", "pending-team-code", rtNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatalf("create empty managed team (member_count 0): %v", err)
	}
	deadTeam := invite(rolling, "dead-team@test.test", uuid.NullUUID{UUID: team.ID, Valid: true})
	liveUpcoming := invite(upcoming, "live-upcoming@test.test", uuid.NullUUID{})
	rtExec(t, db, `UPDATE event_participants SET status = 2 WHERE event_id = $1`, finished)
	accepted := invite(finished, "accepted@test.test", uuid.NullUUID{})
	rtExec(t, db, `UPDATE event_participants SET status = 2 WHERE event_id = $1 AND user_id = $2`, finished, accepted)

	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgeExpiredEventInvitations(ctx, cutoff, batch) }); n != 1 {
		t.Fatalf("expired invitations purged = %d, want only the started team invitation", n)
	}
	for _, id := range []uuid.UUID{liveRolling, liveUpcoming, accepted} {
		if rtCount(t, db, `SELECT count(*) FROM event_participants WHERE user_id = $1`, id) != 1 {
			t.Fatalf("participation of %s must stay", id)
		}
	}
	if rtCount(t, db, `SELECT count(*) FROM event_participants WHERE user_id = $1`, deadTeam) != 0 {
		t.Fatal("the team invitation after the start must be removed")
	}
	// A pending invitation of the finished event (approved above) is kept; a
	// fresh pending one there is removed.
	stale := invite(finished, "stale@test.test", uuid.NullUUID{})
	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgeExpiredEventInvitations(ctx, cutoff, batch) }); n != 1 {
		t.Fatalf("second run purged = %d, want the finished event's pending invitation", n)
	}

	if stale == uuid.Nil || captain == uuid.Nil {
		t.Fatal("seed ids must be set")
	}
}

// One rule for every unconfirmed account (invited or self sign-up): it goes
// 30 days after its creation or its last (re)sent invitation, whichever is
// later; a team it leads passes to a confirmed member, or goes when nobody
// is left.
func TestRetentionPurgesUnconfirmedAccounts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	participants := participantRepo.New(db.Queries)
	teams := eventTeamRepo.New(db.Queries)
	cutoff := rtNow.AddDate(0, 0, -30)
	old := cutoff.AddDate(0, 0, -1)

	finished := rtScheduledEvent(t, db, "unconffinished", rtNow.AddDate(0, 0, -20), false)
	upcoming := rtScheduledEvent(t, db, "unconfupcoming", rtNow.AddDate(0, 1, 0), true)
	manager := mustSeedUser(t, db, "unconf-manager@test.test")
	rtExec(t, db, `UPDATE users SET status = 'active'`)

	user := func(email, status string, createdAt time.Time) uuid.UUID {
		id := mustSeedUser(t, db, email)
		rtExec(t, db, `UPDATE users SET status = $2, created_at = $3 WHERE id = $1`, id, status, createdAt)
		return id
	}
	invite := func(eventID, userID uuid.UUID, team uuid.NullUUID) {
		if _, ok, err := participants.Invite(ctx, eventID, userID, manager, team, rtNow); err != nil || !ok {
			t.Fatalf("invite: ok=%v err=%v", ok, err)
		}
	}
	selfSignUp := user("self-old@test.test", "incomplete", old)
	rtDispatch(t, db, selfSignUp, rtNow)
	confirmed := user("confirmed-old@test.test", "active", old)
	recent := user("self-recent@test.test", "incomplete", rtNow.AddDate(0, 0, -3))
	// Invited long ago but the link was resent 3 days ago: the clock restarted.
	resent := user("resent@test.test", "incomplete", old)
	invite(upcoming, resent, uuid.NullUUID{})
	if _, err := userRepo.New(db.Queries).MarkInvitationSent(ctx, resent, rtNow.AddDate(0, 0, -3)); err != nil {
		t.Fatal(err)
	}
	// A still pending invitation alone keeps nothing past the 30 days.
	staleLive := user("stale-live@test.test", "incomplete", old)
	invite(upcoming, staleLive, uuid.NullUUID{})
	deadInvite := user("dead-invite@test.test", "incomplete", old)
	invite(finished, deadInvite, uuid.NullUUID{})

	// A team led by an unconfirmed invitee with one confirmed member, and an
	// empty team led by another one.
	pendingCaptain := user("pending-captain@test.test", "incomplete", old)
	member := user("member@test.test", "active", old)
	led, err := eventTeamModel.NewManaged(finished, pendingCaptain, "Led Team", "led-team-join-code", rtNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = teams.Create(ctx, led); err != nil {
		t.Fatal(err)
	}
	invite(finished, pendingCaptain, uuid.NullUUID{UUID: led.ID, Valid: true})
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 1)`, finished, member, rtNow, led.ID)
	rtExec(t, db, `UPDATE event_teams SET member_count = 1 WHERE id = $1`, led.ID)
	lonelyCaptain := user("lonely-captain@test.test", "incomplete", old)
	lonely, err := eventTeamModel.NewManaged(finished, lonelyCaptain, "Lonely Team", "lonely-team-join-code", rtNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = teams.Create(ctx, lonely); err != nil {
		t.Fatal(err)
	}

	n := rtDrain(t, 2, func() (int64, error) { return repo.PurgeUnconfirmedAccounts(ctx, cutoff, rtNow, 2) })
	if n != 5 {
		t.Fatalf("purged = %d, want the self sign-up, both old invitees and both pending captains", n)
	}
	for _, gone := range []uuid.UUID{selfSignUp, deadInvite, staleLive, pendingCaptain, lonelyCaptain} {
		if rtCount(t, db, `SELECT count(*) FROM users WHERE id = $1`, gone) != 0 {
			t.Fatalf("unconfirmed account %s must be removed", gone)
		}
		if rtCount(t, db, `SELECT count(*) FROM event_participants WHERE user_id = $1`, gone) != 0 {
			t.Fatalf("pending rows of %s must go with it", gone)
		}
	}
	if rtCount(t, db, `SELECT count(*) FROM notification_dispatches WHERE recipient_user_id = $1`, selfSignUp) != 0 {
		t.Fatal("the delivery journal of a removed account must go with it")
	}
	for _, kept := range []uuid.UUID{confirmed, recent, resent, member} {
		if rtCount(t, db, `SELECT count(*) FROM users WHERE id = $1`, kept) != 1 {
			t.Fatalf("account %s must stay", kept)
		}
	}
	if rtCount(t, db, `SELECT count(*) FROM event_teams WHERE id = $1 AND captain_id = $2`, led.ID, member) != 1 {
		t.Fatal("the led team must pass to its confirmed member")
	}
	if rtCount(t, db, `SELECT count(*) FROM event_participants WHERE user_id = $1 AND team_id = $2 AND team_role = 0`, member, led.ID) != 1 {
		t.Fatal("the new captain must hold the captain role")
	}
	if rtCount(t, db, `SELECT count(*) FROM event_teams WHERE id = $1`, lonely.ID) != 0 {
		t.Fatal("a team with nobody left must be removed")
	}
	if n, err := repo.PurgeUnconfirmedAccounts(ctx, cutoff, rtNow, 2); err != nil || n != 0 {
		t.Fatalf("re-run: n=%d err=%v, want idempotent", n, err)
	}
}
