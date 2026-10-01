package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// «Стенди»: the status log is written by the stand trigger, the telemetry
// sums per-device peaks, the VPN usage sums sessions; the moderators team is
// never counted.
func TestEventAnalytics_Stands(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anstands")
	period := eventAnalyticsModel.Period{From: anStart, To: anStart.Add(4 * time.Hour)}

	for _, team := range []uuid.UUID{f.team, f.moderators} {
		rtExec(t, db, `INSERT INTO event_team_stands (event_team_id, event_id, status, generation, created_at, updated_at, status_changed_at) VALUES ($1, $2, 1, 1, $3, $3, $3)`,
			team, f.event, anStart)
	}
	rtExec(t, db, `UPDATE event_team_stands SET status = 3, reason = 'boom', status_changed_at = $2 WHERE event_team_id = $1`, f.team, anStart.Add(time.Minute))
	rtExec(t, db, `UPDATE event_team_stands SET status = 2, reason = NULL, status_changed_at = $2 WHERE event_team_id = $1`, f.team, anStart.Add(3*time.Minute))

	teams, err := repo.StandTeams(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, teams, 1, "the moderators team is not a stand")
	require.Equal(t, "Blue", teams[0].TeamName)
	require.EqualValues(t, 2, teams[0].Status)

	log, err := repo.StandTransitions(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, log, 3)
	history := eventAnalyticsModel.AnalyzeStandTransitions(log)[f.team]
	require.NotNil(t, history.DeploySeconds)
	require.EqualValues(t, 180, *history.DeploySeconds)
	require.Len(t, history.Failures, 1)
	seconds, recovered := history.Failures[0].RecoverySeconds()
	require.True(t, recovered)
	require.EqualValues(t, 120, seconds)
	require.Equal(t, "boom", history.Failures[0].Reason)

	observe := func(seq int, at time.Time, payload string) {
		rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'group', 'agent', $4, $5, $5, 1, false, $6)`, uuid.Must(uuid.NewV7()), f.event, f.team, seq, at, payload)
	}
	observe(1, anStart.Add(time.Minute), `{"labs":[{"name":"web","status":{"devices":[{"name":"app","usageAvailable":true,"cpuMillicores":"200","memoryBytes":"1000","restartCount":0},{"name":"db","usageAvailable":false,"restartCount":1}]}}]}`)
	observe(2, anStart.Add(2*time.Minute), `{"labs":[{"name":"web","status":{"devices":[{"name":"app","usageAvailable":true,"cpuMillicores":"350","memoryBytes":"900","restartCount":2}]}}]}`)
	resources, err := repo.StandResources(ctx, f.event, period)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.Equal(t, eventAnalyticsRepo.StandResources{TeamID: f.team, Devices: 2, PeakCPUMillis: 350, PeakMemoryBytes: 1000, Restarts: 3, RestartedDevices: 2}, resources[0])

	// The period bounds the telemetry.
	none, err := repo.StandResources(ctx, f.event, eventAnalyticsModel.Period{From: anStart.Add(time.Hour), To: anStart.Add(2 * time.Hour)})
	require.NoError(t, err)
	require.Empty(t, none)

	for i, team := range []uuid.UUID{f.team, f.team, f.moderators} {
		rtExec(t, db, `INSERT INTO event_vpn_sessions (id, event_id, team_id, user_id, client_name, started_at, ended_at, rx_min, rx_max, tx_min, tx_max)
VALUES ($1, $2, $3, $4, 'p-x', $5, $6, 100, 600, 0, 40)`, uuid.Must(uuid.NewV7()), f.event, team, f.user, anStart.Add(time.Duration(i)*time.Hour), anStart.Add(time.Duration(i)*time.Hour+10*time.Minute))
	}
	usage, err := repo.VPNUsage(ctx, f.event, period)
	require.NoError(t, err)
	require.Len(t, usage, 1)
	require.EqualValues(t, 2, usage[0].Sessions)
	require.EqualValues(t, 1, usage[0].Users)
	require.EqualValues(t, 1200, usage[0].Seconds)
	require.EqualValues(t, 1000, usage[0].RxBytes)
	require.EqualValues(t, 80, usage[0].TxBytes)
}

// «Доброчесність»: the facts of the per-solve signals. Hidden teams and the
// moderators team are out; each solve carries the access evidence of its team;
// the flag kind is inferred from the teams' expected flags.
func TestEventAnalytics_IntegrityFacts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anintegrity")
	rtExec(t, db, `UPDATE event_teams SET admitted_manually = true WHERE id = $1`, f.team)
	rtExec(t, db, `UPDATE event_challenges SET snapshot = '{"name":"Web","difficulty":"easy","attachments":[{"file_id":"7d444840-9dc0-4a1b-8f0b-0f0c8b9b8a01","name":"a.zip"}]}' WHERE id = $1`, f.challenge)

	// Nothing recorded yet: no collector start.
	facts, err := repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Nil(t, facts.CollectorStart)
	require.Empty(t, facts.Solves)

	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, anStart.Add(time.Minute))
	rtExec(t, db, `INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at, data) VALUES ($1, $2, $3, 'attempt_rejected', $4, $5, '{"reason":"rate_limit"}')`,
		f.event, f.user, f.team, f.challenge, anStart.Add(2*time.Minute))
	rtExec(t, db, `INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $3), ($2, $3)`, f.teamChallenge, f.modChallenge, anStart.Add(3*time.Minute))
	opened := anStart.Add(30 * time.Second)
	rtExec(t, db, `INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at) VALUES ($1, $2, $3, 'task_opened', $4, $5), ($1, $2, $3, 'task_opened', $4, $6)`,
		f.event, f.user, f.team, f.challenge, opened, opened.Add(time.Minute))
	rtExec(t, db, `INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at, data) VALUES ($1, $2, $3, 'attachment_downloaded', $4, $5, '{"file_id":"7d444840-9dc0-4a1b-8f0b-0f0c8b9b8a01"}')`,
		f.event, f.user, f.team, f.challenge, anStart.Add(45*time.Second))

	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.NotNil(t, facts.CollectorStart)
	require.True(t, facts.CollectorStart.Equal(opened))
	require.Len(t, facts.Attempts, 1)
	require.Equal(t, f.teamChallenge, facts.Attempts[0].TeamChallengeID)
	require.Equal(t, "ICE{?}", facts.Attempts[0].Answer)
	require.False(t, facts.Attempts[0].Correct)
	require.Len(t, facts.Rejections, 1)
	require.Equal(t, eventAnalyticsModel.RateLimitReason, facts.Rejections[0].Reason)
	require.Equal(t, f.challenge, facts.Rejections[0].ChallengeID)
	require.Len(t, facts.Solves, 1, "the moderators team is never judged")
	solve := facts.Solves[0]
	require.Equal(t, f.team, solve.TeamID)
	require.Equal(t, f.teamChallenge, solve.TeamChallengeID)
	require.Equal(t, "Blue", solve.TeamName)
	require.Equal(t, "easy", solve.Level)
	require.Equal(t, 1, solve.AttachmentCount)
	require.False(t, solve.HasLab)
	require.NotNil(t, solve.FirstOpen)
	require.True(t, solve.FirstOpen.Equal(opened), "the earliest open")
	require.NotNil(t, solve.FirstFile)
	require.Nil(t, solve.FirstHint)
	require.Nil(t, solve.FirstVPN)
	// The moderators team holds the same expected flag: two teams, one flag.
	require.Equal(t, eventAnalyticsModel.FlagStatic, solve.Flag)

	// A hint unlock and a VPN session show up as evidence.
	hint := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO team_challenge_hint_unlocks (team_challenge_id, hint_id, event_id, event_team_id, event_challenge_id, unlocked_by, unlocked_at, cost) VALUES ($1, $2, $3, $4, $5, $6, $7, 0)`,
		f.teamChallenge, hint, f.event, f.team, f.challenge, f.user, anStart.Add(time.Minute))
	rtExec(t, db, `INSERT INTO event_vpn_sessions (id, event_id, team_id, user_id, client_name, started_at, ended_at, rx_min, rx_max, tx_min, tx_max)
VALUES ($1, $2, $3, $4, 'p-x', $5, $6, 0, 10, 0, 10)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.user, anStart.Add(2*time.Minute), anStart.Add(9*time.Minute))
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.NotNil(t, facts.Solves[0].FirstHint)
	require.NotNil(t, facts.Solves[0].FirstVPN)
	require.True(t, facts.Solves[0].FirstVPN.Equal(anStart.Add(2*time.Minute)))

	// A lab binding marks a lab task.
	require.False(t, facts.Solves[0].HasLab)
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at) VALUES ($1, $2, $3, $4, 'g', 'l', 2, $5)`,
		uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, anStart)
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.True(t, facts.Solves[0].HasLab)

	// Different expected flags: dynamic.
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{other}' WHERE id = $1`, f.modChallenge)
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Equal(t, eventAnalyticsModel.FlagDynamic, facts.Solves[0].Flag)

	// A hidden team is left out of every list.
	rtExec(t, db, `UPDATE event_teams SET hidden = true WHERE id = $1`, f.team)
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Empty(t, facts.Solves)
	require.Empty(t, facts.Attempts)
	require.Empty(t, facts.Rejections)
}

// The organizer's «перевірено» note: only a team task of the event can be
// reviewed; the note is replaced on a second save and removed with the solve.
func TestEventAnalytics_SolveReviews(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anreviews")
	reviewer := mustSeedUser(t, db, "anreviews-owner@test.test")
	otherEvent := anFixture{event: uuid.Must(uuid.NewV7())}

	saved, err := repo.SaveSolveReview(ctx, f.event, uuid.Must(uuid.NewV7()), reviewer, "x", anStart)
	require.NoError(t, err)
	require.False(t, saved, "not a team task of the event")

	saved, err = repo.SaveSolveReview(ctx, otherEvent.event, f.teamChallenge, reviewer, "x", anStart)
	require.NoError(t, err)
	require.False(t, saved, "a team task of another event")

	saved, err = repo.SaveSolveReview(ctx, f.event, f.teamChallenge, reviewer, "first", anStart)
	require.NoError(t, err)
	require.True(t, saved)
	saved, err = repo.SaveSolveReview(ctx, f.event, f.teamChallenge, reviewer, "second", anStart.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, saved)
	reviews, err := repo.SolveReviews(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, "second", reviews[0].Note)
	require.Equal(t, reviewer, reviews[0].ReviewedBy)
	require.NotEmpty(t, reviews[0].ReviewedByName)
	none, err := repo.SolveReviews(ctx, otherEvent.event)
	require.NoError(t, err)
	require.Empty(t, none)

	deleted, err := repo.DeleteSolveReview(ctx, otherEvent.event, f.teamChallenge)
	require.NoError(t, err)
	require.False(t, deleted)
	deleted, err = repo.DeleteSolveReview(ctx, f.event, f.teamChallenge)
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = repo.DeleteSolveReview(ctx, f.event, f.teamChallenge)
	require.NoError(t, err)
	require.False(t, deleted)
}

// cross_flag: a value equal to ANOTHER team's expected flag, not the team's
// own; a flag shared by several team tasks is skipped; hidden teams are out.
func TestEventAnalytics_IntegrityCrossFlags(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "ancross")
	rtExec(t, db, `UPDATE event_teams SET admitted_manually = true WHERE id = $1`, f.team)
	rtExec(t, db, `UPDATE event_challenges SET snapshot = '{"name":"Web","difficulty":"hard"}' WHERE id = $1`, f.challenge)
	victimUser := mustSeedUser(t, db, "ancross-victim@test.test")
	victim := eaTeam(t, db, f.event, victimUser, "Red")
	victimTC, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: victim, EventChallengeID: f.challenge,
		Snapshot: []byte(`{}`), ExpectedFlag: "ICE{red-secret}", Readiness: 2, CreatedAt: anStart,
	})
	require.NoError(t, err)
	// Blue's own flag differs too; the moderators team holds the shared ICE{x} no more.
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{blue-secret}' WHERE id = $1`, f.teamChallenge)
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{mod-secret}' WHERE id = $1`, f.modChallenge)

	submit := func(answer string, at time.Duration) {
		_, err := db.Queries.CreateChallengeAttempt(ctx, postgres.CreateChallengeAttemptParams{
			ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: f.team, TeamChallengeID: f.teamChallenge,
			UserID: f.user, Answer: answer, Correct: false, ReceivedAt: anStart.Add(at), CreatedAt: anStart.Add(at),
		})
		require.NoError(t, err)
	}
	submit("ICE{red-secret}", time.Minute)
	submit("  ICE{red-secret}  ", 2*time.Minute)
	submit("ICE{blue-secret}", 3*time.Minute) // its own flag: fine
	submit("ICE{nothing}", 4*time.Minute)

	facts, err := repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, facts.CrossFlags, 2)
	got := facts.CrossFlags[0]
	require.Equal(t, f.team, got.TeamID)
	require.Equal(t, f.teamChallenge, got.TeamChallengeID)
	require.Equal(t, victim, got.OwnerTeam.ID)
	require.Equal(t, "Red", got.OwnerTeam.Name)
	require.Equal(t, f.challenge, got.OwnerChallenge)
	require.Equal(t, "hard", got.Level)
	require.NotEqual(t, uuid.Nil, got.ExerciseID)
	require.NotEqual(t, uuid.Nil, got.TaskID)
	_ = victimTC

	// The same value on two team tasks is a shared flag: nobody is flagged.
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{red-secret}' WHERE id = $1`, f.modChallenge)
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Empty(t, facts.CrossFlags)

	// A team holding the value itself is never flagged for it.
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{mod-secret}' WHERE id = $1`, f.modChallenge)
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{red-secret}' WHERE id = $1`, f.teamChallenge)
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Empty(t, facts.CrossFlags)

	// A hidden submitter is out.
	rtExec(t, db, `UPDATE team_challenges SET expected_flag = 'ICE{blue-secret}' WHERE id = $1`, f.teamChallenge)
	rtExec(t, db, `UPDATE event_teams SET hidden = true WHERE id = $1`, f.team)
	facts, err = repo.IntegrityFacts(ctx, f.event)
	require.NoError(t, err)
	require.Empty(t, facts.CrossFlags)
}

// Dismissals: a team task names the catalog task; scope event stays in the
// event, scope exercise reaches other events that use the exercise; saving
// twice is a no-op; a dismissal of another event cannot be removed.
func TestEventAnalytics_IntegrityDismissals(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "andismiss")
	other := anFixture{event: uuid.Must(uuid.NewV7())}
	by := mustSeedUser(t, db, "andismiss-owner@test.test")

	ok, err := repo.SaveDismissal(ctx, f.event, uuid.Must(uuid.NewV7()), eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityTooFast, "", "", by, uuid.Must(uuid.NewV7()), anStart)
	require.NoError(t, err)
	require.False(t, ok, "not a team task of the event")
	ok, err = repo.SaveDismissal(ctx, other.event, f.teamChallenge, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityTooFast, "", "", by, uuid.Must(uuid.NewV7()), anStart)
	require.NoError(t, err)
	require.False(t, ok, "a team task of another event")

	for i := 0; i < 2; i++ {
		ok, err = repo.SaveDismissal(ctx, f.event, f.teamChallenge, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegritySharedWrong, "ice{decoy}", "known", by, uuid.Must(uuid.NewV7()), anStart)
		require.NoError(t, err)
		require.True(t, ok)
	}
	list, err := repo.Dismissals(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, list, 1, "the same dismissal twice is one")
	require.Equal(t, eventAnalyticsModel.DismissEvent, list[0].Scope)
	require.Equal(t, "ice{decoy}", list[0].Key)
	require.NotEmpty(t, list[0].CreatedByName)
	otherList, err := repo.Dismissals(ctx, other.event)
	require.NoError(t, err)
	require.Empty(t, otherList, "an event scope stays in the event")

	// The exercise scope reaches every event that uses the exercise.
	var exercise, task uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT ee.exercise_id, c.task_id FROM team_challenges tc JOIN event_challenges c ON c.id = tc.event_challenge_id
JOIN event_exercises ee ON ee.id = c.event_exercise_id WHERE tc.id = $1`, f.teamChallenge).Scan(&exercise, &task))
	ok, err = repo.SaveDismissal(ctx, f.event, f.teamChallenge, eventAnalyticsModel.DismissExercise, eventAnalyticsModel.IntegrityNoAccess, "", "", by, uuid.Must(uuid.NewV7()), anStart.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	list, err = repo.Dismissals(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, eventAnalyticsModel.DismissExercise, list[0].Scope, "newest first")
	require.Equal(t, exercise, list[0].ExerciseID)
	require.Equal(t, task, list[0].TaskID)

	deleted, err := repo.DeleteDismissal(ctx, other.event, list[1].ID)
	require.NoError(t, err)
	require.False(t, deleted, "an event dismissal of another event")
	deleted, err = repo.DeleteDismissal(ctx, f.event, list[1].ID)
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = repo.DeleteDismissal(ctx, f.event, list[1].ID)
	require.NoError(t, err)
	require.False(t, deleted)
}

// «Звіт»: the ranking counts admitted, visible teams; the task table and the
// funnel tail count everything but the moderators team.
func TestEventAnalytics_Report(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "anreport")
	rtExec(t, db, `UPDATE event_teams SET admitted_manually = true WHERE id = $1`, f.team)
	rtExec(t, db, `UPDATE event_challenges SET published = true WHERE id = $1`, f.challenge)

	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(2*time.Minute))
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, anStart.Add(time.Minute))
	rtExec(t, db, `INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $3), ($2, $3)`, f.teamChallenge, f.modChallenge, anStart.Add(2*time.Minute))
	rtExec(t, db, `INSERT INTO event_activity (event_id, user_id, team_id, kind, subject_id, at) VALUES ($1, $2, $3, 'task_opened', $4, $5)`,
		f.event, f.user, f.team, f.challenge, anStart)

	ranking, err := repo.ReportRanking(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, ranking, 1)
	require.Equal(t, "Blue", ranking[0].Name)
	require.EqualValues(t, 1, ranking[0].Solved)
	require.EqualValues(t, 2, ranking[0].Attempts)
	require.NotNil(t, ranking[0].LastSolveAt)

	tasks, err := repo.ReportTasks(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, f.challenge, tasks[0].ChallengeID)
	require.EqualValues(t, 1, tasks[0].TeamsOpened)
	require.EqualValues(t, 1, tasks[0].TeamsAttempted)
	require.EqualValues(t, 2, tasks[0].Attempts)
	require.EqualValues(t, 1, tasks[0].CorrectAttempts)
	require.EqualValues(t, 1, tasks[0].Solves)
	require.Equal(t, "Blue", tasks[0].FirstSolveTeam)
	require.NotNil(t, tasks[0].FirstSolveAt)
	require.True(t, tasks[0].FirstSolveAt.Equal(anStart.Add(2*time.Minute)))

	funnel, err := repo.ReportFunnel(ctx, f.event)
	require.NoError(t, err)
	require.Equal(t, eventAnalyticsRepo.ReportFunnel{ParticipantsOpened: 1, ParticipantsAttempted: 1, TeamsAttempted: 1, TeamsSolved: 1}, funnel)
}
