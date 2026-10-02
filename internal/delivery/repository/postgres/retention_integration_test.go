package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var rtNow = time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC)

func rtExec(t *testing.T, db *testhelpers.TestDB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func rtCount(t *testing.T, db *testhelpers.TestDB, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

// rtDrain repeats a batched purge the way the job does and returns the total.
func rtDrain(t *testing.T, batch int32, purge func() (int64, error)) int64 {
	t.Helper()
	var total int64
	for {
		n, err := purge()
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		total += n
		if n < int64(batch) {
			return total
		}
	}
}

// rtFinishedEvent seeds an event whose effective finish is finishedAt.
func rtFinishedEvent(t *testing.T, db *testhelpers.TestDB, tag string, finishedAt time.Time) uuid.UUID {
	t.Helper()
	event := mustSeedEventForParticipants(t, db, tag)
	rtExec(t, db, `
UPDATE events
SET lifecycle_configured = true, publish_at = $2, start_at = $2, finish_at = $3, withdraw_at = $4
WHERE id = $1`, event.ID, finishedAt.AddDate(0, 0, -7), finishedAt, finishedAt.AddDate(1, 0, 0))
	return event.ID
}

func rtFormAnswer(t *testing.T, db *testhelpers.TestDB, eventID, userID uuid.UUID) {
	t.Helper()
	formID := uuid.Must(uuid.NewV7())
	rtExec(t, db, `
INSERT INTO event_forms (id, event_id, title, enabled, required, created_at, updated_at)
VALUES ($1, $2, 'Registration form', true, true, $3, $3)
ON CONFLICT DO NOTHING`, formID, eventID, rtNow)
	rtExec(t, db, `
INSERT INTO event_form_versions (id, form_id, event_id, version, enabled, required, document, created_at)
VALUES ($1, $1, $2, 1, true, true, '{"blocks":[]}', $3)`, formID, eventID, rtNow)
	rtExec(t, db, `
INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at)
VALUES ($1, $2, $3, '{"phone":"+380000000000"}', $4)`, eventID, userID, formID, rtNow)
}

func rtDispatch(t *testing.T, db *testhelpers.TestDB, userID uuid.UUID, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	rtExec(t, db, `
INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, created_at, updated_at)
VALUES ($1, 'password_reset', $2, 'sent', $3, $3)`, id, userID, createdAt)
	rtExec(t, db, `
INSERT INTO notification_dispatch_targets (dispatch_id, channel, status, recipient, updated_at)
VALUES ($1, 'email', 'sent', 'person@test.test', $2)`, id, createdAt)
	return id
}

// Every time-based purge deletes exactly the rows past the policy cutoff, in
// batches, and a second run deletes nothing (idempotent).
func TestRetentionPurges_DeleteOnlyRowsPastTheirPeriod(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	userID := mustSeedUser(t, db, "retention-time@test.test")
	const batch = 2

	// Sessions: 90 days after expiry.
	cutoff := rtNow.AddDate(0, 0, -90)
	for _, expiresAt := range []time.Time{cutoff.Add(-72 * time.Hour), cutoff.Add(-48 * time.Hour), cutoff.Add(-time.Hour), cutoff.Add(time.Hour)} {
		rtExec(t, db, `INSERT INTO sessions (id, user_id, expires_at, last_seen, created_at, metadata)
VALUES ($1, $2, $3, $3, $3, '{"ip":"203.0.113.7","user_agent":"test"}')`, uuid.Must(uuid.NewV7()), userID, expiresAt)
	}
	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgeExpiredSessions(ctx, cutoff, batch) }); n != 3 {
		t.Fatalf("expired sessions purged = %d, want 3", n)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID); left != 1 {
		t.Fatalf("sessions left = %d, want the one still inside its period", left)
	}

	// Lab telemetry: 90 days, event observations and platform capacity.
	event := rtFinishedEvent(t, db, "retentionlab", rtNow.AddDate(0, -1, 0))
	team, err := eventTeamModel.New(event, userID, "Lab Team", "retention-lab-code", rtNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	for i, observedAt := range []time.Time{cutoff.Add(-time.Hour), cutoff.Add(time.Hour)} {
		rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'group', 'agent', $4, $5, $5, 1, true, '{}')`, uuid.Must(uuid.NewV7()), event, team.ID, i+1, observedAt)
		rtExec(t, db, `
INSERT INTO platform_lab_capacity_observations (id, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, 'agent', $2, $3, $3, 1, true, '{}')`, uuid.Must(uuid.NewV7()), i+1, observedAt)
	}
	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgeEventLabObservations(ctx, cutoff, batch) }); n != 1 {
		t.Fatalf("event lab observations purged = %d, want 1", n)
	}
	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgePlatformLabCapacityObservations(ctx, cutoff, batch) }); n != 1 {
		t.Fatalf("capacity observations purged = %d, want 1", n)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM event_lab_observations`) + rtCount(t, db, `SELECT count(*) FROM platform_lab_capacity_observations`); left != 2 {
		t.Fatalf("observations left = %d, want the two recent ones", left)
	}

	// Delivery journal: 180 days, targets (recipient address) go with it.
	logCutoff := rtNow.AddDate(0, 0, -180)
	oldDispatch := rtDispatch(t, db, userID, logCutoff.Add(-time.Hour))
	rtDispatch(t, db, userID, logCutoff.Add(time.Hour))
	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgeNotificationDispatches(ctx, logCutoff, batch) }); n != 1 {
		t.Fatalf("dispatches purged = %d, want 1", n)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM notification_dispatch_targets WHERE dispatch_id = $1`, oldDispatch); left != 0 {
		t.Fatal("the purged dispatch's targets must be deleted with it")
	}
	if left := rtCount(t, db, `SELECT count(*) FROM notification_dispatch_targets`); left != 1 {
		t.Fatalf("targets left = %d, want the recent dispatch's", left)
	}

	// Registration answers: event end + 1 year; an event that has not ended
	// (or ended less than a year ago) keeps them.
	answersCutoff := rtNow.AddDate(-1, 0, 0)
	longEnded := rtFinishedEvent(t, db, "retentionold", answersCutoff.AddDate(0, 0, -1))
	recentlyEnded := rtFinishedEvent(t, db, "retentionnew", answersCutoff.AddDate(0, 0, 1))
	manuallyEnded := rtFinishedEvent(t, db, "retentionmanual", rtNow.AddDate(0, 1, 0))
	rtExec(t, db, `UPDATE events SET manual_finished_at = start_at WHERE id = $1`, manuallyEnded)
	rtExec(t, db, `UPDATE events SET start_at = $2, publish_at = $2 WHERE id = $1`, manuallyEnded, answersCutoff.AddDate(0, -1, 0))
	rtExec(t, db, `UPDATE events SET manual_finished_at = start_at + interval '1 day' WHERE id = $1`, manuallyEnded)
	for _, e := range []uuid.UUID{longEnded, recentlyEnded, manuallyEnded} {
		rtFormAnswer(t, db, e, userID)
	}
	if n := rtDrain(t, batch, func() (int64, error) { return repo.PurgeEventFormAnswers(ctx, answersCutoff, batch) }); n != 2 {
		t.Fatalf("form answers purged = %d, want the long-ended and the manually finished event's", n)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM event_form_answers WHERE event_id = $1`, recentlyEnded); left != 1 {
		t.Fatal("answers of an event that ended less than a year ago must stay")
	}

	// Idempotent: nothing is left to purge.
	for name, purge := range map[string]func() (int64, error){
		"sessions":   func() (int64, error) { return repo.PurgeExpiredSessions(ctx, cutoff, batch) },
		"lab":        func() (int64, error) { return repo.PurgeEventLabObservations(ctx, cutoff, batch) },
		"capacity":   func() (int64, error) { return repo.PurgePlatformLabCapacityObservations(ctx, cutoff, batch) },
		"dispatches": func() (int64, error) { return repo.PurgeNotificationDispatches(ctx, logCutoff, batch) },
		"answers":    func() (int64, error) { return repo.PurgeEventFormAnswers(ctx, answersCutoff, batch) },
	} {
		if n, err := purge(); err != nil || n != 0 {
			t.Fatalf("%s second run: n=%d err=%v, want nothing", name, n, err)
		}
	}
}

// A deleted account loses its residual personal data, while its competition
// results stay: the team, participation, attempts and the tombstone user row
// remain, and the scoreboard shows the generic placeholder instead of the
// pseudonym.
func TestRetentionPurgeDeletedAccounts_AnonymizesResults(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	users := userRepo.New(db.Queries)

	event := rtFinishedEvent(t, db, "retentionanon", rtNow.AddDate(0, 0, -1))
	if _, err := eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(event, rtNow)); err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `UPDATE event_configs SET allow_pseudonyms = true WHERE event_id = $1`, event)

	gone := mustSeedUser(t, db, "retention-gone@test.test")
	kept := mustSeedUser(t, db, "retention-kept@test.test")
	var teamIDs []uuid.UUID
	pseudonyms := map[uuid.UUID]string{gone: "ZeroCool", kept: "AcidBurn"}
	for _, userID := range []uuid.UUID{gone, kept} {
		team, err := eventTeamModel.NewIndividual(event, userID, "Solo-"+userID.String(), uuid.Must(uuid.NewV4()).String(), rtNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
			t.Fatal(err)
		}
		teamIDs = append(teamIDs, team.ID)
		rtExec(t, db, `
INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role, pseudonym)
VALUES ($1, $2, 1, $3, $4, 1, $5)`, event, userID, rtNow, team.ID, pseudonyms[userID])
		rtFormAnswer(t, db, event, userID)
		rtDispatch(t, db, userID, rtNow)
		rtExec(t, db, `INSERT INTO in_app_notifications (id, user_id, title) VALUES ($1, $2, 'Hello')`, uuid.Must(uuid.NewV7()), userID)
		rtExec(t, db, `INSERT INTO user_vpn_configs (id, user_id, scope, config, created_at, updated_at) VALUES ($1, $2, 'platform', 'wg', $3, $3)`, uuid.Must(uuid.NewV7()), userID, rtNow)
		rtExec(t, db, `INSERT INTO notification_user_settings (user_id, notification_type, channel, enabled) VALUES ($1, 'flag_accepted', 'email', false)`, userID)
		rtExec(t, db, `INSERT INTO event_managers (event_id, user_id, role, created_at) VALUES ($1, $2, 1, $3)`, event, userID, rtNow)
		rtExec(t, db, `INSERT INTO event_participant_presence (event_id, user_id, last_seen_at) VALUES ($1, $2, $3)`, event, userID, rtNow)
		rtExec(t, db, `INSERT INTO temporal_codes (id, code, type, data, expires_at) VALUES ($1, $2, 1, $3, $4)`,
			uuid.Must(uuid.NewV7()), "code-"+userID.String(), `{"UserID":"`+userID.String()+`","Email":"new@test.test"}`, rtNow.Add(time.Hour))
	}
	publicName := func(teamID, captain uuid.UUID) string {
		var name string
		if err := db.Pool.QueryRow(ctx, `SELECT event_team_public_name(true, $1, $2, t.name) FROM event_teams t WHERE t.id = $3`, event, captain, teamID).Scan(&name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	if got := publicName(teamIDs[0], gone); got != "ZeroCool" {
		t.Fatalf("before purge the pseudonym is shown, got %q", got)
	}

	// The deletion request (domain soft delete) runs first.
	u, err := users.GetByID(ctx, gone)
	if err != nil {
		t.Fatal(err)
	}
	expected := u.UpdatedAt
	if err = u.SoftDelete(rtNow); err != nil {
		t.Fatal(err)
	}
	if _, err = users.Update(ctx, u, expected); err != nil {
		t.Fatal(err)
	}

	if n, err := repo.PurgeDeletedAccounts(ctx, rtNow, 10); err != nil || n != 1 {
		t.Fatalf("PurgeDeletedAccounts: n=%d err=%v, want the one deleted account", n, err)
	}
	for _, table := range []string{"event_form_answers", "in_app_notifications", "user_vpn_configs", "notification_user_settings", "event_managers", "event_participant_presence"} {
		if left := rtCount(t, db, `SELECT count(*) FROM `+table+` WHERE user_id = $1`, gone); left != 0 {
			t.Errorf("%s: %d rows of the deleted account left", table, left)
		}
		if left := rtCount(t, db, `SELECT count(*) FROM `+table+` WHERE user_id = $1`, kept); left != 1 {
			t.Errorf("%s: the active account's row must stay, got %d", table, left)
		}
	}
	// A one-time code of the deleted account (an email change code holds the new address) goes with it.
	if left := rtCount(t, db, `SELECT count(*) FROM temporal_codes WHERE data ->> 'UserID' = $1`, gone.String()); left != 0 {
		t.Errorf("temporal codes of the deleted account left: %d", left)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM temporal_codes WHERE data ->> 'UserID' = $1`, kept.String()); left != 1 {
		t.Errorf("the active account's code must stay, got %d", left)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM notification_dispatches WHERE recipient_user_id = $1`, gone); left != 0 {
		t.Errorf("delivery journal of the deleted account left: %d", left)
	}

	// Results stay, anonymized.
	if got := publicName(teamIDs[0], gone); got != "Учасник" {
		t.Fatalf("scoreboard name after purge = %q, want the anonymized placeholder", got)
	}
	if got := publicName(teamIDs[1], kept); got != "AcidBurn" {
		t.Fatalf("the active participant keeps the pseudonym, got %q", got)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_participants WHERE user_id = $1 AND team_id = $2`, gone, teamIDs[0]); n != 1 {
		t.Fatal("the participation that carries the result must stay")
	}
	if n := rtCount(t, db, `SELECT count(*) FROM users WHERE id = $1 AND personal_data_purged_at IS NOT NULL AND email LIKE 'deleted+%' AND first_name = ''`, gone); n != 1 {
		t.Fatal("the tombstone user row must stay, scrubbed and marked purged")
	}
	board, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event})
	if err != nil {
		t.Fatalf("ListEventScoreboard after purge: %v", err)
	}
	shown := false
	for _, row := range board {
		if row.TeamID == teamIDs[0] {
			shown = true
			if row.TeamName != "Учасник" {
				t.Fatalf("scoreboard row of the deleted account = %q", row.TeamName)
			}
		}
	}
	if !shown {
		t.Fatalf("the deleted account's result must stay on the scoreboard: %+v", board)
	}

	// Idempotent.
	if n, err := repo.PurgeDeletedAccounts(ctx, rtNow, 10); err != nil || n != 0 {
		t.Fatalf("second purge: n=%d err=%v, want nothing", n, err)
	}
}

// The inactive-account statements: warn only accounts past the inactivity
// period (never a super_admin), forget the warning of a returning user, and
// list for deletion only accounts warned before the grace cutoff and unseen
// since.
func TestRetentionInactivityQueries(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	inactiveSince := rtNow.AddDate(-3, 0, 0)
	warnedBefore := rtNow.AddDate(0, 0, -30)

	seed := func(email string, lastSeen time.Time) uuid.UUID {
		id := mustSeedUser(t, db, email)
		rtExec(t, db, `UPDATE users SET last_seen = $2, first_name = 'Name' WHERE id = $1`, id, lastSeen)
		return id
	}
	dormant := seed("dormant@test.test", inactiveSince.Add(-time.Hour))
	seed("recent@test.test", inactiveSince.Add(time.Hour))
	admin := seed("admin@test.test", inactiveSince.Add(-time.Hour))
	rtExec(t, db, `UPDATE users SET role = $2 WHERE id = $1`, admin, string(rbac.RoleSuperAdmin))

	toWarn, err := repo.ListInactiveAccountsToWarn(ctx, inactiveSince, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(toWarn) != 1 || toWarn[0].UserID != dormant || toWarn[0].FirstName != "Name" {
		t.Fatalf("to warn = %+v, want only the dormant non-admin account", toWarn)
	}

	warnedAt := warnedBefore.Add(-time.Hour)
	if ok, err := repo.MarkInactivityWarned(ctx, dormant, warnedAt); err != nil || !ok {
		t.Fatalf("MarkInactivityWarned: ok=%v err=%v", ok, err)
	}
	if ok, _ := repo.MarkInactivityWarned(ctx, dormant, warnedAt); ok {
		t.Fatal("a second mark must not move the warning")
	}
	if again, _ := repo.ListInactiveAccountsToWarn(ctx, inactiveSince, 10); len(again) != 0 {
		t.Fatalf("a warned account is not warned again: %+v", again)
	}

	// A returning user who was warned: last_seen moves past the warning.
	returning := seed("returning@test.test", inactiveSince.Add(-time.Hour))
	rtExec(t, db, `UPDATE users SET inactivity_warned_at = $2 WHERE id = $1`, returning, warnedAt)
	rtExec(t, db, `UPDATE users SET last_seen = $2 WHERE id = $1`, returning, warnedAt.Add(time.Hour))

	toDelete, err := repo.ListInactiveAccountsToDelete(ctx, warnedBefore, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(toDelete) != 1 || toDelete[0].UserID != dormant || !toDelete[0].WarnedAt.Equal(warnedAt) {
		t.Fatalf("to delete = %+v, want only the dormant account", toDelete)
	}
	if n, err := repo.ClearReturnedInactivityWarnings(ctx); err != nil || n != 1 {
		t.Fatalf("ClearReturnedInactivityWarnings: n=%d err=%v, want the returning user", n, err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM users WHERE id = $1 AND inactivity_warned_at IS NULL`, returning); n != 1 {
		t.Fatal("the returning user's warning must be cleared")
	}
	// Not due yet: warned inside the grace period.
	if early, _ := repo.ListInactiveAccountsToDelete(ctx, warnedAt, 10); len(early) != 0 {
		t.Fatalf("an account inside its grace period is not deleted: %+v", early)
	}
}

func TestRetentionPurgeExpiredTemporalCodes(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	user := mustSeedUser(t, db, "retention-codes@test.test")
	for name, expires := range map[string]time.Time{"old": rtNow.Add(-time.Hour), "older": rtNow.Add(-48 * time.Hour), "live": rtNow.Add(time.Hour)} {
		rtExec(t, db, `INSERT INTO temporal_codes (id, code, type, data, expires_at) VALUES ($1, $2, 1, $3, $4)`,
			uuid.Must(uuid.NewV7()), name, `{"UserID":"`+user.String()+`"}`, expires)
	}
	if n := rtDrain(t, 1, func() (int64, error) { return repo.PurgeExpiredTemporalCodes(ctx, rtNow, 1) }); n != 2 {
		t.Fatalf("purged %d, want the two expired codes", n)
	}
	if left := rtCount(t, db, `SELECT count(*) FROM temporal_codes WHERE code = 'live'`); left != 1 {
		t.Fatal("a code that has not expired must stay")
	}
}
