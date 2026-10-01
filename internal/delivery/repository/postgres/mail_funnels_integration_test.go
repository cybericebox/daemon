package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// mfParticipant inserts a participation row; the user is created at userAt.
func mfParticipant(t *testing.T, db *testhelpers.TestDB, event uuid.UUID, tag string, userStatus string, userAt time.Time, row string, args ...any) uuid.UUID {
	t.Helper()
	user := mustSeedUser(t, db, tag+"@test.test")
	rtExec(t, db, `UPDATE users SET status = $2, created_at = $3 WHERE id = $1`, user, userStatus, userAt)
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, `+row, append([]any{event, user}, args...)...)
	return user
}

// The mail funnels read only data that exists: invitations, applications and
// the invitee accounts. Moderators and hidden teams are never counted.
func TestMailFunnels_EventAndPlatformScope(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "mfunnel")
	day := func(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, time.UTC) }
	const invCols = `status, created_at, invited, invitation_sent_at, decided_at, decided_by) VALUES ($1, $2, $3, $4, true, $5, $6, NULL)`
	const appCols = `status, created_at, decided_at, decided_by) VALUES ($1, $2, $3, $4, $5, $6)`

	// Invitations: accepted after 1 h by an account the invitation created; accepted
	// after 4 h by an existing account; still pending for an unfinished account.
	mfParticipant(t, db, f.event, "mf-inv1", "active", day(1, 10, 0), invCols, 2, day(1, 10, 0), day(1, 10, 0), day(1, 11, 0))
	mfParticipant(t, db, f.event, "mf-inv2", "active", day(1, 8, 0).AddDate(-1, 0, 0), invCols, 2, day(1, 10, 0), day(1, 10, 0), day(1, 14, 0))
	mfParticipant(t, db, f.event, "mf-inv3", "incomplete", day(1, 10, 0), invCols, 1, day(1, 10, 0), day(1, 10, 0), nil)
	// A moderator invited to the moderators team is not counted.
	modInvited := mfParticipant(t, db, f.event, "mf-inv4", "active", day(1, 10, 0), invCols, 2, day(1, 10, 0), day(1, 10, 0), day(1, 10, 5))
	rtExec(t, db, `UPDATE event_participants SET team_id = $3, team_role = 0 WHERE event_id = $1 AND user_id = $2`, f.event, modInvited, f.moderators)

	// Applications: approved after 1 min, rejected after 3 min, undecided; open
	// registration (approved on the spot, nobody decided) is not an application.
	mfParticipant(t, db, f.event, "mf-app1", "active", day(2, 9, 0), appCols, 2, day(2, 9, 0), day(2, 9, 1), f.user)
	mfParticipant(t, db, f.event, "mf-app2", "active", day(2, 9, 0), appCols, 3, day(2, 9, 0), day(2, 9, 3), f.user)
	mfParticipant(t, db, f.event, "mf-app3", "active", day(2, 9, 0), appCols, 1, day(2, 9, 0), nil, nil)
	mfParticipant(t, db, f.event, "mf-open", "active", day(2, 9, 0), appCols, 2, day(2, 9, 0), day(2, 9, 0), nil)

	// Another event adds to the platform figures only.
	other := mustSeedEventForParticipants(t, db, "mfunnelb")
	otherUser := mfParticipant(t, db, other.ID, "mf-other", "active", day(3, 9, 0), invCols, 2, day(3, 9, 0), day(3, 9, 0), day(3, 9, 30))

	events := eventAnalyticsRepo.New(db.Queries)
	got, err := events.MailFunnels(ctx, f.event, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.InvitationsSent != 3 || got.InvitationsAccepted != 2 || got.InvitationAcceptSamples != 2 || got.InvitationAcceptMedianSeconds != 9000 {
		t.Fatalf("invitations: %+v", got)
	}
	if got.RegistrationsStarted != 2 || got.RegistrationsCompleted != 1 {
		t.Fatalf("registration: %+v", got)
	}
	if got.ApplicationsSubmitted != 3 || got.ApplicationsApproved != 1 || got.ApplicationsRejected != 1 ||
		got.ApplicationDecisionSamples != 2 || got.ApplicationDecisionMedianSeconds != 120 {
		t.Fatalf("applications: %+v", got)
	}

	from, to := day(2, 0, 0), day(3, 0, 0)
	if got, err = events.MailFunnels(ctx, f.event, &from, &to); err != nil || got.InvitationsSent != 0 || got.ApplicationsSubmitted != 3 {
		t.Fatalf("a window applies to the creation time: %+v %v", got, err)
	}

	// A hidden team's member is left out too.
	rtExec(t, db, `UPDATE event_teams SET hidden = true WHERE id = $1`, f.team)
	hidden := mfParticipant(t, db, f.event, "mf-hidden", "active", day(2, 9, 0), appCols, 2, day(2, 9, 0), day(2, 9, 1), f.user)
	rtExec(t, db, `UPDATE event_participants SET team_id = $3, team_role = 1 WHERE event_id = $1 AND user_id = $2`, f.event, hidden, f.team)
	if got, err = events.MailFunnels(ctx, f.event, nil, nil); err != nil || got.ApplicationsSubmitted != 3 {
		t.Fatalf("hidden team member counted: %+v %v", got, err)
	}

	// Platform scope: every event, and the accounts created in the window.
	platform := platformAnalyticsRepo.New(db.Queries)
	rtExec(t, db, `UPDATE users SET created_at = $2, status = 'active' WHERE id = $1`, f.user, time.Date(2020, 1, 10, 0, 0, 0, 0, time.UTC))
	rtExec(t, db, `UPDATE users SET created_at = $2, status = 'incomplete' WHERE id = $1`, otherUser, time.Date(2020, 1, 11, 0, 0, 0, 0, time.UTC))
	all, err := platform.MailFunnels(ctx, day(1, 0, 0), day(30, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if all.InvitationsSent != 4 || all.InvitationsAccepted != 3 || all.ApplicationsSubmitted != 3 {
		t.Fatalf("platform figures: %+v", all)
	}
	old, err := platform.MailFunnels(ctx, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if old.RegistrationsStarted != 2 || old.RegistrationsCompleted != 1 || old.InvitationsSent != 0 {
		t.Fatalf("platform accounts: %+v", old)
	}
}

// The journal shows the recipient user of a dispatch by name and email.
func TestJournalRowsCarryTheRecipientName(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	named := mustSeedUser(t, db, "mf-named@test.test")
	rtExec(t, db, `UPDATE users SET first_name = 'Ann', last_name = 'Lee' WHERE id = $1`, named)
	bare := mustSeedUser(t, db, "mf-bare@test.test")
	rtExec(t, db, `UPDATE users SET first_name = '', last_name = '' WHERE id = $1`, bare)
	ids := map[uuid.UUID]uuid.UUID{}
	for i, user := range []uuid.UUID{named, bare} {
		id := uuid.Must(uuid.NewV7())
		ids[id] = user
		rtExec(t, db, `INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, created_at, updated_at) VALUES ($1, 'x', $2, 'done', now() + $3 * interval '1 second', now())`, id, user, i)
		rtExec(t, db, `INSERT INTO notification_dispatch_targets (dispatch_id, channel, status, recipient) VALUES ($1, 'in_app', 'done', '')`, id)
	}
	repo := dispatchRepo.New(db.Queries)
	rows, _, err := repo.List(ctx, dispatchModel.ListDispatchesFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, r := range rows {
		names[r.RecipientEmail] = r.RecipientName
	}
	if names["mf-named@test.test"] != "Ann Lee" || names["mf-bare@test.test"] != "" {
		t.Fatalf("names = %v", names)
	}
	for id, user := range ids {
		one, err := repo.Get(ctx, id)
		if err != nil || one.RecipientUserID != user {
			t.Fatalf("get: %+v %v", one, err)
		}
		if user == named && one.RecipientName != "Ann Lee" {
			t.Fatalf("get name = %q", one.RecipientName)
		}
	}
}
