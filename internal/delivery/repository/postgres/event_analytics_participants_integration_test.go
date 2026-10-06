package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func apExec(t *testing.T, db *testhelpers.TestDB, sql string, args ...any) {
	t.Helper()
	rtExec(t, db, sql, args...)
}

// «Учасники»: the funnel, registration channels, team fill, the registration
// answers and the drop-off list, all without the moderators team.
func TestEventAnalytics_Participants(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anpeople")
	day := func(d int) time.Time { return time.Date(2026, 9, d, 12, 0, 0, 0, time.UTC) }

	// f.user: approved (open), in team Blue, with an effectively correct attempt.
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(time.Minute))
	apExec(t, db, `UPDATE event_participants SET created_at = $3 WHERE event_id = $1 AND user_id = $2`, f.event, f.user, day(25))

	invited := mustSeedUser(t, db, "anpeople-invited@test.test")
	apExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, invited) VALUES ($1, $2, 1, $3, true)`, f.event, invited, day(26))
	pending := mustSeedUser(t, db, "anpeople-pending@test.test")
	apExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 1, $3)`, f.event, pending, day(26))
	decided := mustSeedUser(t, db, "anpeople-decided@test.test")
	apExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, decided_by, decided_at) VALUES ($1, $2, 2, $3, $4, $3)`, f.event, decided, day(27), f.user)
	// Approved, no team, never attempted, opened one task: the drop-off list.
	apExec(t, db, `INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at) VALUES ($1, $2, NULL, 'task_opened', $3, $4)`, f.event, decided, f.challenge, day(28))
	// The moderators' own row is never counted.
	moderator := mustSeedUser(t, db, "anpeople-mod@test.test")
	apExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`, f.event, moderator, day(27), f.moderators)

	funnel, err := repo.ParticipantFunnel(ctx, f.event)
	if err != nil {
		t.Fatal(err)
	}
	want := eventAnalyticsRepo.ParticipantFunnel{Invited: 1, Registered: 3, Approved: 2, InTeam: 1, Attempted: 1, Solved: 1}
	if funnel != want {
		t.Fatalf("funnel = %+v, want %+v", funnel, want)
	}

	days, err := repo.RegistrationDays(ctx, f.event, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		day     int
		channel string
	}
	got := map[key]int64{}
	for _, d := range days {
		got[key{d.Day.Day(), d.Channel}] = d.Registrations
	}
	if len(got) != 4 || got[key{25, "open"}] != 1 || got[key{26, "invitation"}] != 1 || got[key{26, "approval"}] != 1 || got[key{27, "approval"}] != 1 {
		t.Fatalf("registrations = %v", got)
	}
	from, to := day(26), day(27)
	if days, err = repo.RegistrationDays(ctx, f.event, &from, &to); err != nil || len(days) != 2 {
		t.Fatalf("windowed registrations: %v %v", days, err)
	}

	teams, err := repo.TeamFill(ctx, f.event)
	if err != nil || len(teams) != 1 || teams[0].Name != "Blue" || teams[0].ID != f.team {
		t.Fatalf("team fill (no moderators team): %+v %v", teams, err)
	}

	dropOffs, total, err := repo.DropOffs(ctx, f.event, 10)
	if err != nil || total != 1 || len(dropOffs) != 1 || dropOffs[0].UserID != decided || dropOffs[0].OpenedTasks != 1 || dropOffs[0].ApprovedAt == nil {
		t.Fatalf("drop-offs: %+v total %d %v", dropOffs, total, err)
	}
	if _, total, _ = repo.DropOffs(ctx, f.event, 0); total != 0 {
		t.Log("a zero limit returns no rows and so no total")
	}

	// The registration form: two versions; each participant's latest answers.
	form := uuid.Must(uuid.NewV7())
	v1, v2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	apExec(t, db, `INSERT INTO event_forms (id, event_id, title, enabled, required, purpose, created_at, updated_at) VALUES ($1, $2, 'Registration', true, false, 'registration', $3, $3)`, form, f.event, day(1))
	apExec(t, db, `INSERT INTO event_form_versions (id, event_id, version, enabled, required, document, created_at, form_id)
VALUES ($1, $3, 1, true, false, '{"blocks":[{"id":"c","type":"field","key":"city","input":"select","label":"City","options":["Kyiv"]}]}', $4, $5),
       ($2, $3, 2, true, false, '{"blocks":[{"id":"c","type":"field","key":"city","input":"select","label":"City","options":["Kyiv","Lviv"]}]}', $4, $5)`, v1, v2, f.event, day(1), form)
	apExec(t, db, `INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at) VALUES
 ($1, $2, $3, '{"city":"Kyiv"}', $6), ($1, $2, $4, '{"city":"Lviv"}', $7), ($1, $5, $3, '{"city":"Kyiv"}', $6), ($1, $8, $3, '{"city":"Kyiv"}', $6)`,
		f.event, f.user, v1, v2, decided, day(2), day(3), moderator)

	versions, err := repo.RegistrationFormVersions(ctx, f.event)
	if err != nil || len(versions) != 2 || versions[1].Version != 2 || len(versions[1].Blocks) != 1 || versions[1].Blocks[0].Key != "city" {
		t.Fatalf("versions: %+v %v", versions, err)
	}
	answers, err := repo.RegistrationAnswers(ctx, f.event)
	if err != nil || len(answers) != 2 {
		t.Fatalf("latest answers per participant, moderators left out: %+v %v", answers, err)
	}
	seen := map[uuid.UUID]string{}
	for _, a := range answers {
		seen[a.VersionID], _ = a.Answers["city"].(string)
	}
	if seen[v2] != "Lviv" || seen[v1] != "Kyiv" {
		t.Fatalf("answers = %v", seen)
	}
}

// «Комунікації»: dispatch targets and in-app notifications of the event only,
// and the delivery figures of every form.
func TestEventAnalytics_Communications(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "ancomms")
	other := mustSeedEventForParticipants(t, db, "ancommsother")
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	dispatch := func(eventID uuid.UUID, typ string, created time.Time, targets ...[2]string) {
		id := uuid.Must(uuid.NewV7())
		apExec(t, db, `INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, created_at, scope_event_id) VALUES ($1, $2, $3, 'done', $4, $5)`, id, typ, f.user, created, eventID)
		for _, tg := range targets {
			apExec(t, db, `INSERT INTO notification_dispatch_targets (dispatch_id, channel, status) VALUES ($1, $2, $3)`, id, tg[0], tg[1])
		}
	}
	dispatch(f.event, "event.start", at, [2]string{"email", "done"}, [2]string{"in_app", "done"})
	dispatch(f.event, "event.start", at, [2]string{"email", "error"})
	dispatch(f.event, "team.invite", at.AddDate(0, 0, 5), [2]string{"email", "done"})
	dispatch(other.ID, "event.start", at, [2]string{"email", "done"})

	stats, err := repo.DispatchStats(ctx, f.event, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, s := range stats {
		counts[s.Type+"/"+s.Channel+"/"+s.Status] = s.Targets
	}
	if len(counts) != 4 || counts["event.start/email/done"] != 1 || counts["event.start/email/error"] != 1 || counts["event.start/in_app/done"] != 1 || counts["team.invite/email/done"] != 1 {
		t.Fatalf("dispatch stats: %v", counts)
	}
	until := at.AddDate(0, 0, 1)
	if stats, err = repo.DispatchStats(ctx, f.event, nil, &until); err != nil || len(stats) != 3 {
		t.Fatalf("windowed dispatch stats: %+v %v", stats, err)
	}

	notify := func(eventID uuid.UUID, read bool) {
		var readAt *time.Time
		if read {
			readAt = &at
		}
		apExec(t, db, `INSERT INTO in_app_notifications (id, user_id, title, notification_type, scope_event_id, read_at, created_at) VALUES ($1, $2, 'T', 'event.start', $3, $4, $5)`,
			uuid.Must(uuid.NewV7()), f.user, eventID, readAt, at)
	}
	notify(f.event, true)
	notify(f.event, false)
	notify(other.ID, true)
	inApp, err := repo.InAppStats(ctx, f.event, nil, nil)
	if err != nil || len(inApp) != 1 || inApp[0].Type != "event.start" || inApp[0].Total != 2 || inApp[0].Read != 1 {
		t.Fatalf("in-app stats: %+v %v", inApp, err)
	}

	form := uuid.Must(uuid.NewV7())
	version := uuid.Must(uuid.NewV7())
	assignment := uuid.Must(uuid.NewV7())
	apExec(t, db, `INSERT INTO event_forms (id, event_id, title, enabled, required, created_at, updated_at) VALUES ($1, $2, 'Feedback', true, false, $3, $3)`, form, f.event, at)
	apExec(t, db, `INSERT INTO event_form_versions (id, event_id, version, enabled, required, document, created_at, form_id) VALUES ($1, $2, 1, true, false, '{"blocks":[]}', $3, $4)`, version, f.event, at, form)
	apExec(t, db, `INSERT INTO event_form_assignments (id, event_id, form_id, trigger, audience, presentation, created_at, updated_at) VALUES ($1, $2, $3, 'manual', '{"kind":"all_participants"}', 'banner', $4, $4)`, assignment, f.event, form, at)
	second := mustSeedUser(t, db, "ancomms-second@test.test")
	apExec(t, db, `INSERT INTO event_form_deliveries (form_version_id, user_id, assignment_id, presentation, created_at, completed_at) VALUES ($1, $2, $3, 'banner', $4, $4), ($1, $5, $3, 'banner', $4, NULL)`, version, f.user, assignment, at, second)

	forms, err := repo.FormCompletion(ctx, f.event, nil, nil)
	if err != nil || len(forms) != 1 || forms[0].Title != "Feedback" || forms[0].Assigned != 2 || forms[0].Completed != 1 || forms[0].Purpose != "other" {
		t.Fatalf("form completion: %+v %v", forms, err)
	}
	after := at.Add(time.Hour)
	if forms, err = repo.FormCompletion(ctx, f.event, &after, nil); err != nil || forms[0].Assigned != 0 {
		t.Fatalf("windowed form completion: %+v %v", forms, err)
	}
}
