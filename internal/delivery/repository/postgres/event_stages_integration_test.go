package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cybericebox/daemon/internal/delivery/repository/challengeAttemptRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// stT0 is the start of the seeded event (anSeed: 10:00-14:00).
var stT0 = anStart

func stH(h float64) time.Time { return stT0.Add(time.Duration(h * float64(time.Hour))) }

func stMustStage(t *testing.T, db *testhelpers.TestDB, event uuid.UUID, name string, open, close float64, returnable bool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $4, $4)`,
		id, event, name, stH(open), stH(close), returnable)
	return id
}

func stPgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// The SQL phase function is the single definition every query uses; it must agree with the Go mirror at every
// boundary, for both values of returnable and for a set without a stage.
func TestEventStagePhaseSQLMatchesGo(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	opens, closes := stH(2), stH(4)
	for _, returnable := range []bool{false, true} {
		for _, at := range []time.Time{opens.Add(-time.Nanosecond * 1000), opens, stH(3), closes.Add(-time.Microsecond), closes, closes.Add(time.Microsecond), stH(40)} {
			var phase int16
			if err := db.Pool.QueryRow(ctx, `SELECT event_stage_phase($1, $2, $3, $4)`, opens, closes, returnable, at).Scan(&phase); err != nil {
				t.Fatal(err)
			}
			if want := eventModel.PhaseOf(opens, closes, returnable, at); phase != int16(want) {
				t.Errorf("returnable=%v at=%v: sql=%d go=%d", returnable, at, phase, want)
			}
		}
	}
	var phase int16
	if err := db.Pool.QueryRow(ctx, `SELECT event_stage_phase(NULL, NULL, NULL, $1)`, stH(1)).Scan(&phase); err != nil || phase != int16(eventModel.StagePhaseOpen) {
		t.Fatalf("a set without a stage is open: phase=%d err=%v", phase, err)
	}
}

func TestEventStageConstraintsAndEpoch(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "stageschema")
	other := mustSeedEventForParticipants(t, db, "stageother")

	one := stMustStage(t, db, f.event, "One", 0, 1, true)
	stMustStage(t, db, f.event, "Two", 1, 2, false) // adjacent is allowed
	_, err := db.Pool.Exec(ctx, `INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, created_at, updated_at) VALUES ($1, $2, 'Clash', $3, $4, false, $3, $3)`,
		uuid.Must(uuid.NewV7()), f.event, stH(1.5), stH(3))
	if stPgCode(err) != pgerrcode.ExclusionViolation {
		t.Fatalf("overlap must be refused by the exclusion constraint: %v", err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, created_at, updated_at) VALUES ($1, $2, ' one ', $3, $4, false, $3, $3)`,
		uuid.Must(uuid.NewV7()), f.event, stH(5), stH(6))
	if stPgCode(err) != pgerrcode.UniqueViolation {
		t.Fatalf("the name is unique per event, case and space insensitive: %v", err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, created_at, updated_at) VALUES ($1, $2, 'Backwards', $3, $3, false, $3, $3)`,
		uuid.Must(uuid.NewV7()), f.event, stH(5))
	if stPgCode(err) != pgerrcode.CheckViolation {
		t.Fatalf("opens_at < closes_at: %v", err)
	}
	// the same window is fine for another event
	stMustStage(t, db, other.ID, "One", 0, 1, false)

	// a set can only use a stage of its own event
	set := uuid.Nil
	if err = db.Pool.QueryRow(ctx, `SELECT event_exercise_id FROM event_challenges WHERE id = $1`, f.challenge).Scan(&set); err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `UPDATE event_exercises SET stage_id = $2 WHERE id = $1`, set, one)
	if err != nil {
		t.Fatalf("own stage: %v", err)
	}
	var foreign uuid.UUID
	if err = db.Pool.QueryRow(ctx, `SELECT id FROM event_stages WHERE event_id = $1`, other.ID).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE event_exercises SET stage_id = $2 WHERE id = $1`, set, foreign); stPgCode(err) != pgerrcode.ForeignKeyViolation {
		t.Fatalf("a set must not use another event's stage: %v", err)
	}
	// a stage that still has sets cannot be deleted; with none it can
	if _, err = db.Queries.DeleteEventStage(ctx, postgres.DeleteEventStageParams{ID: one, EventID: f.event}); stPgCode(err) != pgerrcode.ForeignKeyViolation {
		t.Fatalf("a stage with sets must not be deleted: %v", err)
	}
	if n, err := db.Queries.CountEventStageSets(ctx, uuid.NullUUID{UUID: one, Valid: true}); err != nil || n != 1 {
		t.Fatalf("sets of the stage = %d err=%v", n, err)
	}

	// the epoch counts every opens_at and the closes_at of the stages that are not returnable
	epoch := func(at time.Time) int32 {
		var n int32
		if err := db.Pool.QueryRow(ctx, `SELECT event_stage_epoch($1, $2)`, f.event, at).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for at, want := range map[time.Time]int32{stH(-1): 0, stH(0): 1, stH(1): 2, stH(1.5): 2, stH(2): 3, stH(9): 3} {
		// One (returnable): its close changes nothing; Two (not returnable): opens at 1, closes at 2
		if got := epoch(at); got != want {
			t.Errorf("epoch(%v) = %d, want %d", at, got, want)
		}
	}
	if n := func() int32 {
		var n int32
		if err := db.Pool.QueryRow(ctx, `SELECT event_stage_epoch($1, $2)`, uuid.Must(uuid.NewV7()), stH(3)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}(); n != 0 {
		t.Fatalf("an event without stages has epoch 0, got %d", n)
	}

	// deleting the event cascades to its stages and sets in one statement
	if _, err = db.Pool.Exec(ctx, `DELETE FROM events WHERE id = $1`, f.event); err != nil {
		t.Fatalf("event deletion must cascade through stages and sets: %v", err)
	}
}

func stRowByChallenge(t *testing.T, rows []postgres.ListTeamBoardChallengesRow, id uuid.UUID) (postgres.ListTeamBoardChallengesRow, bool) {
	t.Helper()
	for _, row := range rows {
		if row.EventChallengeID == id {
			return row, true
		}
	}
	return postgres.ListTeamBoardChallengesRow{}, false
}

func stSetOf(t *testing.T, db *testhelpers.TestDB, challenge uuid.UUID) uuid.UUID {
	t.Helper()
	var set uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `SELECT event_exercise_id FROM event_challenges WHERE id = $1`, challenge).Scan(&set); err != nil {
		t.Fatal(err)
	}
	return set
}

// Board read, access read and hint state follow the stage phase at the given moment; the moderators board keeps
// everything; a set without a stage is unaffected.
func TestStagePhaseGatesBoardAccessAndHints(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "stagegates")
	rtExec(t, db, `UPDATE event_challenges SET published = true`)
	set := stSetOf(t, db, f.challenge)
	stage := stMustStage(t, db, f.event, "Gate", 1, 2, false)
	rtExec(t, db, `UPDATE event_exercises SET stage_id = $2 WHERE id = $1`, set, stage)

	cases := []struct {
		name        string
		at          time.Time
		visible     bool
		phase       int16
		closed      bool
		stageReturn bool
	}{
		{"upcoming", stH(0.5), false, 0, false, false},
		{"open", stH(1.5), true, 1, false, false},
		{"closed", stH(3), true, 3, true, false},
	}
	for _, c := range cases {
		board, err := db.Queries.ListTeamBoardChallenges(ctx, postgres.ListTeamBoardChallengesParams{EventTeamID: f.team, PublishedOnly: true, At: c.at})
		if err != nil {
			t.Fatal(err)
		}
		row, found := stRowByChallenge(t, board, f.challenge)
		if found != c.visible {
			t.Fatalf("%s: participant board visible=%v, want %v", c.name, found, c.visible)
		}
		if found && (row.StagePhase != c.phase || !row.StageID.Valid || row.StageID.UUID != stage) {
			t.Fatalf("%s: row phase=%d stage=%v", c.name, row.StagePhase, row.StageID)
		}
		moderators, err := db.Queries.ListTeamBoardChallenges(ctx, postgres.ListTeamBoardChallengesParams{EventTeamID: f.moderators, PublishedOnly: false, At: c.at})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := stRowByChallenge(t, moderators, f.challenge); !ok {
			t.Fatalf("%s: the moderators board keeps everything", c.name)
		}
		access, err := db.Queries.GetEventChallengeAccess(ctx, postgres.GetEventChallengeAccessParams{ID: f.challenge, At: c.at})
		if err != nil || !access.Published || access.Phase != c.phase {
			t.Fatalf("%s: access=%+v err=%v", c.name, access, err)
		}
		hints, err := db.Queries.GetTeamChallengeHints(ctx, postgres.GetTeamChallengeHintsParams{EventTeamID: f.team, EventChallengeID: f.challenge, At: c.at})
		if err != nil || hints.StagePhase != c.phase {
			t.Fatalf("%s: hints phase=%d err=%v", c.name, hints.StagePhase, err)
		}
	}

	// returnable: after the close the task stays open (phase 2)
	rtExec(t, db, `UPDATE event_stages SET returnable = true WHERE id = $1`, stage)
	access, err := db.Queries.GetEventChallengeAccess(ctx, postgres.GetEventChallengeAccessParams{ID: f.challenge, At: stH(3)})
	if err != nil || access.Phase != int16(eventModel.StagePhaseEndedReturnable) {
		t.Fatalf("returnable after close: %+v err=%v", access, err)
	}
	// no stage: always open
	rtExec(t, db, `UPDATE event_exercises SET stage_id = NULL WHERE id = $1`, set)
	if access, err = db.Queries.GetEventChallengeAccess(ctx, postgres.GetEventChallengeAccessParams{ID: f.challenge, At: stH(0.1)}); err != nil || access.Phase != 1 {
		t.Fatalf("no stage: %+v err=%v", access, err)
	}
}

// Categories are derived from the tasks the board returns, so a group with only hidden tasks never appears.
func TestEmptyCategoriesAbsentFromBoard(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "stagegroups")
	rtExec(t, db, `UPDATE event_challenges SET published = true`)
	group := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_challenge_groups (id, event_id, name, order_index, created_at) VALUES ($1, $2, 'Hidden group', 0, $3)`, group, f.event, stT0)
	rtExec(t, db, `UPDATE event_challenges SET group_id = $2 WHERE id = $1`, f.challenge, group)
	stage := stMustStage(t, db, f.event, "Later", 2, 3, false)
	rtExec(t, db, `UPDATE event_exercises SET stage_id = $2 WHERE id = $1`, stSetOf(t, db, f.challenge), stage)
	board, err := db.Queries.ListTeamBoardChallenges(ctx, postgres.ListTeamBoardChallengesParams{EventTeamID: f.team, PublishedOnly: true, At: stH(1)})
	if err != nil || len(board) != 0 {
		t.Fatalf("an upcoming stage's task must not reach the board, so its group is absent: %d rows err=%v", len(board), err)
	}
	board, err = db.Queries.ListTeamBoardChallenges(ctx, postgres.ListTeamBoardChallengesParams{EventTeamID: f.team, PublishedOnly: true, At: stH(2.5)})
	if err != nil || len(board) != 1 || board[0].GroupName != "Hidden group" {
		t.Fatalf("once the stage opens the group shows with its task: %+v err=%v", board, err)
	}
}

// Lab access: the allowed list follows the phase (upcoming and closed are out, returnable keeps access, the
// moderators team is never gated), and the sync wakes at a stage boundary through the epoch alone.
func TestLabAccessFollowsStagesAndWakesAtBoundaries(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "stagelab")
	rtExec(t, db, `UPDATE event_challenges SET published = true`)
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at, deployed_at)
VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, f.group, f.lab, anStart)
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at, deployed_at)
VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`, uuid.Must(uuid.NewV7()), f.event, f.moderators, f.challenge, "mod-"+f.group, f.lab, anStart)
	set := stSetOf(t, db, f.challenge)
	available := func(team uuid.UUID) bool {
		t.Helper()
		labs, err := db.Queries.ListEventLabAccessLabs(ctx, team)
		if err != nil || len(labs) != 1 {
			t.Fatalf("labs=%v err=%v", labs, err)
		}
		return labs[0].Available
	}
	now := time.Now()
	// the policy uses now(): put the stage windows around the real now
	window := func(open, close time.Duration, returnable bool) {
		rtExec(t, db, `UPDATE event_exercises SET stage_id = NULL WHERE id = $1`, set)
		rtExec(t, db, `DELETE FROM event_stages WHERE event_id = $1 AND true`, f.event)
		id := uuid.Must(uuid.NewV7())
		rtExec(t, db, `INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, created_at, updated_at) VALUES ($1, $2, 'Lab', $3, $4, $5, $3, $3)`,
			id, f.event, now.Add(open), now.Add(close), returnable)
		rtExec(t, db, `UPDATE event_exercises SET stage_id = $2 WHERE id = $1`, set, id)
	}
	window(time.Hour, 2*time.Hour, false)
	if available(f.team) {
		t.Fatal("an upcoming stage's lab must be closed")
	}
	if !available(f.moderators) {
		t.Fatal("the moderators team is never gated by a stage")
	}
	window(-time.Hour, time.Hour, false)
	if !available(f.team) {
		t.Fatal("an open stage's lab is open")
	}
	window(-2*time.Hour, -time.Hour, false)
	if available(f.team) {
		t.Fatal("a closed (not returnable) stage's lab access goes to zero")
	}
	window(-2*time.Hour, -time.Hour, true)
	if !available(f.team) {
		t.Fatal("a returnable stage keeps lab access after it closes")
	}

	// the sync: once applied it is clean; a stage boundary alone makes it dirty
	apply := func() {
		t.Helper()
		dirty, err := db.Queries.ListDirtyEventLabAccessSyncs(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range dirty {
			if _, err = db.Queries.MarkEventLabAccessSyncApplied(ctx, postgres.MarkEventLabAccessSyncAppliedParams{EventTeamID: row.EventTeamID, DesiredRevision: row.DesiredRevision,
				RuntimeOpen: row.RuntimeOpen, VpnEnabled: row.VpnEnabled, StageEpoch: row.StageEpoch, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	rtExec(t, db, `UPDATE events SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		f.event, now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(48*time.Hour), now.Add(72*time.Hour))
	rtExec(t, db, `UPDATE event_exercises SET stage_id = NULL WHERE id = $1`, set)
	rtExec(t, db, `DELETE FROM event_stages WHERE event_id = $1 AND true`, f.event)
	if _, err := db.Queries.RequestEventLabAccessSync(ctx, postgres.RequestEventLabAccessSyncParams{EventTeamID: f.team, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	apply()
	dirty := func() int {
		rows, err := db.Queries.ListDirtyEventLabAccessSyncs(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	if dirty() != 0 {
		t.Fatal("an applied sync with no stages is clean")
	}
	// a stage that has already opened changes the epoch: the sync is dirty with no revision or runtime change
	stMustStageAt(t, db, f.event, "Boundary", now.Add(-time.Minute), now.Add(time.Hour), false)
	rows, err := db.Queries.ListDirtyEventLabAccessSyncs(ctx, 100)
	if err != nil || len(rows) != 1 || rows[0].StageEpoch != 1 || rows[0].DesiredRevision != rows[0].AppliedRevision {
		t.Fatalf("the sync must wake on the epoch alone: %+v err=%v", rows, err)
	}
	apply()
	if dirty() != 0 {
		t.Fatal("applying the epoch cleans the sync")
	}
	// a returnable stage closing changes no access: its close is not an epoch boundary
	rtExec(t, db, `UPDATE event_exercises SET stage_id = NULL WHERE id = $1`, set)
	rtExec(t, db, `DELETE FROM event_stages WHERE event_id = $1 AND true`, f.event)
	stMustStageAt(t, db, f.event, "Returnable", now.Add(-2*time.Hour), now.Add(-time.Hour), true)
	apply()
	rtExec(t, db, `UPDATE event_stages SET closes_at = $2 WHERE event_id = $1`, f.event, now.Add(-30*time.Minute))
	if dirty() != 0 {
		t.Fatal("the close of a returnable stage must not wake the sync")
	}
}

func stMustStageAt(t *testing.T, db *testhelpers.TestDB, event uuid.UUID, name string, opens, closes time.Time, returnable bool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $4, $4)`,
		id, event, name, opens, closes, returnable)
	return id
}

// The decay window of a staged set is its stage; an unstaged set keeps the event window.
func TestScoringContextUsesTheStageWindow(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "stagescoring")
	set := stSetOf(t, db, f.challenge)
	row, err := db.Queries.GetTeamChallengeScoringContext(ctx, f.teamChallenge)
	if err != nil || row.StageOpensAt.Valid || row.StageClosesAt.Valid {
		t.Fatalf("unstaged: %+v err=%v", row, err)
	}
	if !row.StartAt.Equal(stT0) {
		t.Fatalf("the event window start = %v", row.StartAt)
	}
	stage := stMustStage(t, db, f.event, "Window", 1, 2, false)
	rtExec(t, db, `UPDATE event_exercises SET stage_id = $2 WHERE id = $1`, set, stage)
	row, err = db.Queries.GetTeamChallengeScoringContext(ctx, f.teamChallenge)
	if err != nil || !row.StageOpensAt.Valid || !row.StageOpensAt.Time.Equal(stH(1)) || !row.StageClosesAt.Valid || !row.StageClosesAt.Time.Equal(stH(2)) {
		t.Fatalf("staged: %+v err=%v", row, err)
	}
	// the repository hands the decay its window: the stage for a staged set, the event for an unstaged one
	scoring, err := challengeAttemptRepo.New(db.Queries).GetScoringContext(ctx, f.teamChallenge)
	if err != nil || !scoring.StartAt.Equal(stH(1)) || scoring.FinishAt == nil || !scoring.FinishAt.Equal(stH(2)) {
		t.Fatalf("staged window: %+v err=%v", scoring, err)
	}
	rtExec(t, db, `UPDATE event_exercises SET stage_id = NULL WHERE id = $1`, set)
	scoring, err = challengeAttemptRepo.New(db.Queries).GetScoringContext(ctx, f.teamChallenge)
	if err != nil || !scoring.StartAt.Equal(stT0) || scoring.FinishAt == nil || !scoring.FinishAt.Equal(stH(4)) {
		t.Fatalf("an unstaged set keeps the event window: %+v err=%v", scoring, err)
	}
}

// Practice attempts never reach a rating read: the effective view the results, scoring, attempt limits,
// integrity and analytics read skips them, practice solves live in their own table, and the team still sees
// its own practice attempts in its history.
func TestPracticeAttemptsNeverReachTheRating(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "stagepractice")
	attempt := func(correct, practice bool, at time.Time) {
		t.Helper()
		if _, err := db.Queries.CreateChallengeAttempt(ctx, postgres.CreateChallengeAttemptParams{
			ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: f.team, TeamChallengeID: f.teamChallenge, UserID: f.user,
			Answer: "ICE{x}", Correct: correct, ReceivedAt: at, CreatedAt: at, Practice: practice}); err != nil {
			t.Fatal(err)
		}
	}
	attempt(false, false, stH(0.1))
	attempt(false, true, stH(0.2))
	attempt(true, true, stH(0.3)) // a correct practice answer
	if err := db.Queries.UpsertTeamChallengePracticeSolve(ctx, postgres.UpsertTeamChallengePracticeSolveParams{TeamChallengeID: f.teamChallenge, SolvedAt: stH(0.3)}); err != nil {
		t.Fatal(err)
	}

	solved, err := db.Queries.GetEffectiveTeamChallengeSolvedAt(ctx, f.teamChallenge)
	if err != nil || solved.Solved {
		t.Fatalf("a practice solve must not be a solve: %+v err=%v", solved, err)
	}
	limit, err := db.Queries.GetTeamChallengeAttemptLimit(ctx, f.teamChallenge)
	if err != nil || limit.Wrong != 1 || limit.Solved {
		t.Fatalf("practice attempts do not count against the attempt limit: %+v err=%v", limit, err)
	}
	var counted, all int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM effective_challenge_attempts WHERE team_challenge_id = $1`, f.teamChallenge).Scan(&counted); err != nil || counted != 1 {
		t.Fatalf("effective attempts = %d err=%v", counted, err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM effective_challenge_attempts_all WHERE team_challenge_id = $1`, f.teamChallenge).Scan(&all); err != nil || all != 3 {
		t.Fatalf("all attempts = %d err=%v", all, err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM team_challenge_solves WHERE team_challenge_id = $1`, f.teamChallenge); n != 0 {
		t.Fatalf("no rated solve exists, found %d", n)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM team_challenge_practice_solves WHERE team_challenge_id = $1`, f.teamChallenge); n != 1 {
		t.Fatalf("practice solve rows = %d", n)
	}
	history, err := db.Queries.ListTeamResultAttempts(ctx, postgres.ListTeamResultAttemptsParams{EventID: f.event, EventTeamID: f.team})
	if err != nil || len(history) != 3 {
		t.Fatalf("the team's own history shows practice attempts: %d err=%v", len(history), err)
	}
	practice := 0
	for _, row := range history {
		if row.Practice {
			practice++
		}
	}
	if practice != 2 {
		t.Fatalf("practice flags in the history = %d", practice)
	}
	if n, err := db.Queries.CountEventSolutionAttempts(ctx, postgres.CountEventSolutionAttemptsParams{EventID: f.event}); err != nil || n != 1 {
		t.Fatalf("the moderators journal counts only rated attempts: %d err=%v", n, err)
	}
	// the practice solve does not satisfy a prerequisite (prerequisites read the rated solves)
	dependent := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_challenges (id, event_exercise_id, task_id, order_index, points, hints_enabled, published, snapshot, created_at)
SELECT $1, event_exercise_id, $2, order_index + 1, points, hints_enabled, true, snapshot, created_at FROM event_challenges WHERE id = $3`, dependent, uuid.Must(uuid.NewV7()), f.challenge)
	rtExec(t, db, `INSERT INTO event_challenge_prerequisites (challenge_id, prerequisite_challenge_id) VALUES ($1, $2)`, dependent, f.challenge)
	if _, err = db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: f.team, EventChallengeID: dependent,
		Snapshot: []byte(`{}`), ExpectedFlag: "ICE{d}", Readiness: 2, CreatedAt: stT0}); err != nil {
		t.Fatal(err)
	}
	prerequisites, err := db.Queries.ListTeamChallengePrerequisites(ctx, f.team)
	if err != nil || len(prerequisites) != 1 || prerequisites[0].Solved {
		t.Fatalf("a practice solve must not satisfy a prerequisite: %+v err=%v", prerequisites, err)
	}
	// the board shows the practice solve to the team
	rtExec(t, db, `UPDATE event_challenges SET published = true`)
	board, err := db.Queries.ListTeamBoardChallenges(ctx, postgres.ListTeamBoardChallengesParams{EventTeamID: f.team, PublishedOnly: true, At: stH(1)})
	if err != nil {
		t.Fatal(err)
	}
	row, ok := stRowByChallenge(t, board, f.challenge)
	if !ok || !row.PracticeSolved || row.SolvedAt.Valid {
		t.Fatalf("board row = %+v ok=%v", row, ok)
	}
}

// The stand engine holds back the labs of a later stage: they are not deployed and not counted by the
// event-start barrier until their sets are due.
func TestStandCountersAndPendingSkipNotDueSets(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "stagestands")
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at)
VALUES ($1, $2, $3, $4, $5, $6, 0, $7)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, f.group, f.lab, anStart)
	set := stSetOf(t, db, f.challenge)
	pending := func(notDue ...uuid.UUID) int {
		rows, err := db.Queries.ListPendingEventLabBindings(ctx, postgres.ListPendingEventLabBindingsParams{EventID: f.event, NotDueExerciseIds: append([]uuid.UUID{}, notDue...)})
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	counter := func(notDue ...uuid.UUID) int64 {
		rows, err := db.Queries.ListStandTeams(ctx, postgres.ListStandTeamsParams{EventID: f.event, NotDueExerciseIds: append([]uuid.UUID{}, notDue...)})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == f.team {
				return row.PendingLabs
			}
		}
		t.Fatal("team not listed")
		return 0
	}
	if pending() != 1 || counter() != 1 {
		t.Fatalf("a due pending lab is deployed and counted: pending=%d counter=%d", pending(), counter())
	}
	if pending(set) != 0 || counter(set) != 0 {
		t.Fatalf("a not due set's lab is neither deployed nor counted: pending=%d counter=%d", pending(set), counter(set))
	}
	if pending(uuid.Must(uuid.NewV7())) != 1 {
		t.Fatal("an unrelated not-due set changes nothing")
	}
}

// 0156 rolls back and applies again cleanly (the view, the columns, the tables and the functions).
func TestEventStagesMigrationDownThenUp(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	run := func(name string) {
		t.Helper()
		sql, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	run("0156_event_stages.down.sql")
	for _, table := range []string{"event_stages", "team_challenge_practice_solves"} {
		if n := rtCount(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, table); n != 0 {
			t.Fatalf("%s after down", table)
		}
	}
	if n := rtCount(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'event_configs' AND column_name = 'stand_deploy_lead_minutes'`); n != 1 {
		t.Fatal("the deploy lead column must come back on down")
	}
	run("0156_event_stages.up.sql")
	if n := rtCount(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'event_configs' AND column_name = 'stand_deploy_lead_minutes'`); n != 0 {
		t.Fatal("the deploy lead column is dropped on up")
	}
}
