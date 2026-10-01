package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func paovDay(month, day int) time.Time {
	return time.Date(2026, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// paovNow is the clock of the seeded platform.
var paovNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// paovWindow: the period [09-20, 09-30) and the previous one [09-10, 09-20).
var paovWindow = platformAnalyticsRepo.OverviewWindow{From: paovDay(9, 20), To: paovDay(9, 30), PrevFrom: paovDay(9, 10), PrevTo: paovDay(9, 20)}

type paovFixture struct {
	anFixture
	u1, u2, u3, u4, u5 uuid.UUID
	events             []uuid.UUID
}

func paovSetUser(t *testing.T, db *testhelpers.TestDB, id uuid.UUID, created time.Time, password bool, provider bool) {
	t.Helper()
	rtExec(t, db, `UPDATE users SET created_at = $2, last_seen = $2 WHERE id = $1`, id, created)
	if password {
		rtExec(t, db, `UPDATE users SET hashed_password = 'x' WHERE id = $1`, id)
	}
	if provider {
		rtExec(t, db, `INSERT INTO user_providers (id, user_id, provider, provider_user_id) VALUES ($1, $2, 'google', $3)`,
			uuid.Must(uuid.NewV7()), id, uuid.Must(uuid.NewV7()).String())
	}
}

func paovParticipant(t *testing.T, db *testhelpers.TestDB, event, user uuid.UUID, status int, created time.Time, decided *time.Time, invited bool) {
	t.Helper()
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, decided_at, invited) VALUES ($1, $2, $3, $4, $5, $6)`,
		event, user, status, created, decided, invited)
}

// paovSeed builds a platform with five events (running, draft, published,
// finished, archived at paovNow), accounts of every sign-in method, sessions,
// participations, buckets, dispatches and stands. Every account starts on
// 2026-01-01; the tests move the ones they need into a window.
func paovSeed(t *testing.T, db *testhelpers.TestDB) paovFixture {
	t.Helper()
	f := paovFixture{anFixture: anSeed(t, db, "paov1")}
	e1 := f.event
	e2 := mustSeedEventForParticipants(t, db, "paov2").ID
	e3 := mustSeedEventForParticipants(t, db, "paov3").ID
	e4 := mustSeedEventForParticipants(t, db, "paov4").ID
	e5 := mustSeedEventForParticipants(t, db, "paov5").ID
	f.events = []uuid.UUID{e1, e2, e3, e4, e5}

	rtExec(t, db, `UPDATE users SET created_at = '2026-01-01T00:00:00Z', last_seen = '2026-01-01T00:00:00Z'`)
	rtExec(t, db, `UPDATE events SET archive_at = NULL, available_from = '2026-01-01T00:00:00Z', created_at = '2026-08-01T00:00:00Z'`)
	rtExec(t, db, `UPDATE events SET created_at = $2 WHERE id = $1`, e1, paovDay(9, 25))
	rtExec(t, db, `UPDATE events SET created_at = $2 WHERE id = $1`, e2, paovDay(9, 12))
	rtExec(t, db, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		e3, paovDay(9, 20), paovDay(10, 10), paovDay(10, 11), paovDay(11, 11))
	rtExec(t, db, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		e4, paovDay(8, 20), paovDay(9, 1), paovDay(9, 2), paovDay(10, 30))
	rtExec(t, db, `UPDATE events SET archive_at = $2 WHERE id = $1`, e5, paovDay(9, 1))

	mk := func(email string) uuid.UUID { return mustSeedUser(t, db, "paov-"+email+"@test.test") }
	f.u1, f.u2, f.u3, f.u4, f.u5 = mk("u1"), mk("u2"), mk("u3"), mk("u4"), mk("u5")
	paovSetUser(t, db, f.u1, paovDay(9, 25), true, true)  // both
	paovSetUser(t, db, f.u2, paovDay(9, 12), true, false) // password only, previous window
	paovSetUser(t, db, f.u3, paovDay(9, 25), false, true) // Google only
	paovSetUser(t, db, f.u4, paovDay(9, 25), true, false) // deleted: never counted
	paovSetUser(t, db, f.u5, paovDay(8, 1), false, false) // no method
	rtExec(t, db, `UPDATE users SET deleted_at = $2 WHERE id = $1`, f.u4, paovDay(9, 26))

	// Activity: u1 signed in on 09-25 and was seen on 09-26, u3 was seen on
	// 09-27, u2 on 09-12 (previous window).
	rtExec(t, db, `INSERT INTO sessions (id, user_id, expires_at, last_seen, created_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.Must(uuid.NewV7()), f.u1, paovDay(10, 25), paovDay(9, 26), paovDay(9, 25))
	rtExec(t, db, `UPDATE users SET last_seen = $2 WHERE id = $1`, f.u1, paovDay(9, 26))
	rtExec(t, db, `UPDATE users SET last_seen = $2 WHERE id = $1`, f.u3, paovDay(9, 27))
	rtExec(t, db, `UPDATE users SET last_seen = $2 WHERE id = $1`, f.u2, paovDay(9, 12))
	rtExec(t, db, `UPDATE users SET last_seen = $2 WHERE id = $1`, f.u4, paovDay(9, 27)) // deleted: not active

	// Participation. anSeed already has `user` approved in e1 (created 09-29 10:00).
	decided := paovDay(9, 26)
	paovParticipant(t, db, e1, f.u1, 2, paovDay(9, 25), &decided, false)
	paovParticipant(t, db, e1, f.u3, 1, paovDay(9, 27), nil, false)
	paovParticipant(t, db, e1, f.u2, 1, paovDay(9, 27), nil, true) // invited, not accepted: not a registration
	d12 := paovDay(9, 12)
	paovParticipant(t, db, e2, f.u1, 2, paovDay(9, 12), &d12, false)
	d5 := paovDay(9, 5)
	paovParticipant(t, db, e3, f.u1, 2, paovDay(9, 5), &d5, false)
	paovParticipant(t, db, e1, f.u4, 2, paovDay(9, 25), &decided, false) // deleted account
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`,
		e1, mustSeedUser(t, db, "paov-mod@test.test"), paovDay(9, 25), f.moderators) // moderators team: never counted
	rtExec(t, db, `UPDATE users SET created_at = '2026-01-01T00:00:00Z', last_seen = '2026-01-01T00:00:00Z' WHERE email = 'paov-mod@test.test'`)

	// Buckets (the moderators team is never in them).
	bucket := func(at time.Time, attempts, solves int) {
		rtExec(t, db, `INSERT INTO event_activity_buckets (event_id, bucket_at, team_id, challenge_id, attempts, correct, solves) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			e1, at, f.team, f.challenge, attempts, solves)
	}
	bucket(paovDay(9, 25).Add(10*time.Hour), 5, 2)
	bucket(paovDay(9, 25).Add(10*time.Hour+5*time.Minute), 1, 0)
	bucket(paovDay(9, 26).Add(9*time.Hour), 3, 1)
	bucket(paovDay(9, 15).Add(9*time.Hour), 4, 1)
	bucket(paovDay(9, 1).Add(9*time.Hour), 9, 9)  // before the previous window
	bucket(paovDay(9, 30).Add(9*time.Hour), 7, 7) // after the window

	// Mail: current window 2 sent, 1 failed; previous 2 sent. SMTP tests, in-app
	// targets and other windows are not counted.
	dispatch := func(kind string, at time.Time, channel, status string) {
		id := uuid.Must(uuid.NewV7())
		rtExec(t, db, `INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'done', $4, $4)`,
			id, kind, f.u1, at)
		rtExec(t, db, `INSERT INTO notification_dispatch_targets (dispatch_id, channel, status) VALUES ($1, $2, $3)`, id, channel, status)
	}
	dispatch("welcome", paovDay(9, 25).Add(time.Hour), "email", "done")
	dispatch("welcome", paovDay(9, 25).Add(2*time.Hour), "email", "done")
	dispatch("welcome", paovDay(9, 27), "email", "error")
	dispatch("welcome", paovDay(9, 27), "in_app", "done")
	dispatch("smtp_test", paovDay(9, 27), "email", "done")
	dispatch("welcome", paovDay(9, 12), "email", "done")
	dispatch("welcome", paovDay(9, 13), "email", "done")
	dispatch("welcome", paovDay(9, 1), "email", "done")

	// Stands: one ready, one failed now; the stand logs the seed triggered are
	// replaced by known transitions.
	rtExec(t, db, `INSERT INTO event_team_stands (event_team_id, event_id, status, generation, created_at, updated_at, status_changed_at) VALUES ($1, $2, 2, 1, $3, $3, $3)`,
		f.team, e1, paovDay(9, 25))
	rtExec(t, db, `INSERT INTO event_team_stands (event_team_id, event_id, status, generation, created_at, updated_at, status_changed_at) VALUES ($1, $2, 3, 1, $3, $3, $3)`,
		f.moderators, e1, paovDay(9, 25))
	rtExec(t, db, `DELETE FROM event_stand_transitions`)
	transition := func(source string, to int, at time.Time) {
		rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, generation, to_status, at) VALUES ($1, $2, $3, 1, $4, $5)`,
			e1, f.team, source, to, at)
	}
	transition("stand", 3, paovDay(9, 25))
	transition("stand", 3, paovDay(9, 26))
	transition("stand", 2, paovDay(9, 26))
	transition("lab", 3, paovDay(9, 26))
	transition("stand", 3, paovDay(9, 12))
	return f
}

func TestPlatformAnalyticsOverview_Counts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	paovSeed(t, db)

	var accounts int64
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	users, err := repo.OverviewUsers(ctx, paovWindow)
	if err != nil {
		t.Fatal(err)
	}
	if users.Total != accounts || users.New != 2 || users.NewPrev != 1 {
		t.Fatalf("users = %+v (accounts %d)", users, accounts)
	}
	active, err := repo.ActiveUsers(ctx, paovWindow)
	if err != nil {
		t.Fatal(err)
	}
	if active.Active != 2 || active.ActivePrev != 1 {
		t.Fatalf("active = %+v, want u1+u3 and u2 (deleted and idle accounts excluded)", active)
	}

	events, err := repo.OverviewEvents(ctx, paovWindow, paovNow)
	if err != nil {
		t.Fatal(err)
	}
	want := platformAnalyticsRepo.OverviewEvents{Draft: 1, Published: 1, Running: 1, Finished: 1, Archived: 1, Total: 5, New: 1, NewPrev: 1}
	if events != want {
		t.Fatalf("events = %+v, want %+v", events, want)
	}

	participants, err := repo.OverviewParticipants(ctx, paovWindow)
	if err != nil {
		t.Fatal(err)
	}
	// Registered: u1@e1, u3@e1, the seeded member and the deleted account's
	// participation (it happened); approved: u1@e1, the member and the deleted
	// account. The invitation and the moderators team are not counted.
	if participants.Registered != 4 || participants.RegisteredPrev != 1 || participants.Approved != 3 || participants.ApprovedPrev != 1 {
		t.Fatalf("participants = %+v", participants)
	}

	activity, err := repo.OverviewActivity(ctx, paovWindow)
	if err != nil {
		t.Fatal(err)
	}
	if activity.Attempts != 9 || activity.Solves != 3 || activity.AttemptsPrev != 4 || activity.SolvesPrev != 1 {
		t.Fatalf("activity = %+v", activity)
	}

	mail, err := repo.OverviewMail(ctx, paovWindow)
	if err != nil {
		t.Fatal(err)
	}
	if mail.Sent != 2 || mail.Failed != 1 || mail.SentPrev != 2 || mail.FailedPrev != 0 {
		t.Fatalf("mail = %+v (smtp_test and in-app targets must not count)", mail)
	}

	stands, err := repo.OverviewStands(ctx, paovWindow)
	if err != nil {
		t.Fatal(err)
	}
	if stands.Ready != 1 || stands.Creating != 0 || stands.FailedNow != 1 || stands.Failures != 2 || stands.FailuresPrev != 1 {
		t.Fatalf("stands = %+v (lab transitions must not count)", stands)
	}
}

func TestPlatformAnalyticsOverview_Empty(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	w := platformAnalyticsRepo.OverviewWindow{From: paovDay(9, 20), To: paovDay(9, 30), PrevFrom: paovDay(9, 30), PrevTo: paovDay(9, 30)}

	if u, err := repo.OverviewUsers(ctx, w); err != nil || u != (platformAnalyticsRepo.OverviewUsers{}) {
		t.Fatalf("users = %+v, %v", u, err)
	}
	if a, err := repo.ActiveUsers(ctx, w); err != nil || a != (platformAnalyticsRepo.OverviewActiveUsers{}) {
		t.Fatalf("active = %+v, %v", a, err)
	}
	if e, err := repo.OverviewEvents(ctx, w, paovNow); err != nil || e != (platformAnalyticsRepo.OverviewEvents{}) {
		t.Fatalf("events = %+v, %v", e, err)
	}
	if p, err := repo.OverviewParticipants(ctx, w); err != nil || p != (platformAnalyticsRepo.OverviewParticipants{}) {
		t.Fatalf("participants = %+v, %v", p, err)
	}
	if a, err := repo.OverviewActivity(ctx, w); err != nil || a != (platformAnalyticsRepo.OverviewActivity{}) {
		t.Fatalf("activity = %+v, %v", a, err)
	}
	if m, err := repo.OverviewMail(ctx, w); err != nil || m != (platformAnalyticsRepo.OverviewMail{}) {
		t.Fatalf("mail = %+v, %v", m, err)
	}
	if s, err := repo.OverviewStands(ctx, w); err != nil || s != (platformAnalyticsRepo.OverviewStands{}) {
		t.Fatalf("stands = %+v, %v", s, err)
	}
	// Series are zero-filled: one point per UTC day.
	days, err := repo.OverviewMailDays(ctx, w.From, w.To)
	if err != nil || len(days) != 10 || !days[0].Day.Equal(w.From) || !days[9].Day.Equal(paovDay(9, 29)) {
		t.Fatalf("mail days = %+v, %v", days, err)
	}
}

func TestPlatformAnalyticsOverview_Series(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	paovSeed(t, db)

	newUsers, err := repo.OverviewUsersDays(ctx, paovWindow.From, paovWindow.To)
	if err != nil || len(newUsers) != 10 {
		t.Fatalf("new users = %+v, %v", newUsers, err)
	}
	for _, d := range newUsers {
		want := int64(0)
		if d.Day.Equal(paovDay(9, 25)) {
			want = 2 // u1 and u3 (u4 was deleted)
		}
		if d.New != want {
			t.Fatalf("new users on %s = %d, want %d", d.Day.Format("01-02"), d.New, want)
		}
	}

	activity, err := repo.OverviewActivityDays(ctx, paovWindow.From, paovWindow.To)
	if err != nil || len(activity) != 10 {
		t.Fatalf("activity = %+v, %v", activity, err)
	}
	got := map[int]platformAnalyticsRepo.OverviewActivityDay{}
	for _, d := range activity {
		got[d.Day.Day()] = d
	}
	if got[25].Attempts != 6 || got[25].Solves != 2 || got[26].Attempts != 3 || got[26].Solves != 1 || got[27].Attempts != 0 {
		t.Fatalf("activity days = %+v", got)
	}

	mail, err := repo.OverviewMailDays(ctx, paovWindow.From, paovWindow.To)
	if err != nil || len(mail) != 10 {
		t.Fatalf("mail = %+v, %v", mail, err)
	}
	if mail[5].Sent != 2 || mail[7].Failed != 1 || mail[7].Sent != 0 {
		t.Fatalf("mail days = %+v", mail)
	}
}
