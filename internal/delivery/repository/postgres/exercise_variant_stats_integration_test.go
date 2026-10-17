package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var (
	vsVariant0 = uuid.FromStringOrNil("0199f000-0000-7000-8000-0000000000a0")
	vsVariant1 = uuid.FromStringOrNil("0199f000-0000-7000-8000-0000000000a1")
	vsTask1    = uuid.FromStringOrNil("0199f000-0000-7000-8000-0000000000b1")
	vsTask2    = uuid.FromStringOrNil("0199f000-0000-7000-8000-0000000000b2")
	vsVersion1 = uuid.FromStringOrNil("0199f000-0000-7000-8000-0000000000c1")
	vsVersion2 = uuid.FromStringOrNil("0199f000-0000-7000-8000-0000000000c2")
)

// vsID is a fixed id for the n-th row a statistics test inserts.
func vsID(n int) uuid.UUID {
	return uuid.FromStringOrNil(fmt.Sprintf("0199f000-0000-7000-8000-%012d", n))
}

// vsVariants is 2 variants × 2 tasks with fixed ids (task ids match per position).
func vsVariants() []exerciseModel.Variant {
	task := func(id uuid.UUID, name string) exerciseModel.Task {
		return exerciseModel.Task{ID: id, Name: name, Description: json.RawMessage(`{"blocks":[]}`),
			Difficulty: exerciseModel.DifficultyEasy, Flag: []string{"ICE{x}"}}
	}
	return []exerciseModel.Variant{
		{ID: vsVariant0, Index: 0, Tasks: []exerciseModel.Task{task(vsTask1, "One"), task(vsTask2, "Two")}},
		{ID: vsVariant1, Index: 1, Tasks: []exerciseModel.Task{task(vsTask1, "One"), task(vsTask2, "Two")}},
	}
}

type vsSeed struct {
	db         *testhelpers.TestDB
	eventID    uuid.UUID
	exerciseID uuid.UUID
	eventExID  uuid.UUID
	challenges [2]uuid.UUID // by task: vsTask1, vsTask2
	next       int
}

// seedVariantStats: a published version (2 variants × 2 tasks) attached to one event with 2 challenges.
func seedVariantStats(t *testing.T, tag string) *vsSeed {
	t.Helper()
	ctx := context.Background()
	db := testhelpers.SetupTestDB(t)
	event := mustSeedEventForParticipants(t, db, tag)
	exercises := exerciseRepo.New(db.Queries)
	exercise := mustCreateExercise(t, exercises, "Variant stats")
	if _, err := exercises.UpsertDraft(ctx, exercise.ID, vsVersion1, exerciseModel.ExerciseVersion{Variants: vsVariants()}, itNow, uuid.NullUUID{}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if _, err := exercises.Publish(ctx, exercise.ID, itNow); err != nil {
		t.Fatalf("publish: %v", err)
	}
	s := &vsSeed{db: db, eventID: event.ID, exerciseID: exercise.ID, next: 1}
	eventExercise, err := db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: s.id(), EventID: event.ID, ExerciseID: exercise.ID, ExerciseVersionID: vsVersion1, CreatedAt: itNow,
	})
	if err != nil {
		t.Fatalf("attach exercise: %v", err)
	}
	s.eventExID = eventExercise.ID
	for i, taskID := range []uuid.UUID{vsTask1, vsTask2} {
		challenge, cErr := db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
			ID: s.id(), EventExerciseID: eventExercise.ID, TaskID: taskID, OrderIndex: int32(i), Points: 100,
			Snapshot: []byte(`{}`), CreatedAt: itNow,
		})
		if cErr != nil {
			t.Fatalf("create challenge: %v", cErr)
		}
		s.challenges[i] = challenge.ID
	}
	return s
}

func (s *vsSeed) id() uuid.UUID {
	s.next++
	return vsID(s.next)
}

func (s *vsSeed) team(t *testing.T, name string, moderators bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	captain := mustSeedUser(t, s.db, "vs-"+name+"@test.test")
	team, err := eventTeamModel.New(s.eventID, captain, name, "variant-stats-code-"+name, itNow)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(s.db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	if moderators {
		if _, err = s.db.Pool.Exec(ctx, `UPDATE event_teams SET moderators = true, hidden = true WHERE id = $1`, team.ID); err != nil {
			t.Fatal(err)
		}
	}
	return team.ID
}

// assign issues the variant to the team on both challenges (one row per subtask).
func (s *vsSeed) assign(t *testing.T, teamID uuid.UUID, variant int32) [2]uuid.UUID {
	t.Helper()
	var out [2]uuid.UUID
	for i, challengeID := range s.challenges {
		row, err := s.db.Queries.CreateTeamChallenge(context.Background(), postgres.CreateTeamChallengeParams{
			ID: s.id(), EventID: s.eventID, EventTeamID: teamID, EventChallengeID: challengeID,
			VariantIndex: variant, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
		})
		if err != nil {
			t.Fatalf("assign: %v", err)
		}
		out[i] = row.ID
	}
	return out
}

func (s *vsSeed) solve(t *testing.T, teamChallengeID uuid.UUID) {
	t.Helper()
	if err := s.db.Queries.UpsertTeamChallengeSolve(context.Background(), postgres.UpsertTeamChallengeSolveParams{
		TeamChallengeID: teamChallengeID, SolvedAt: itNow,
	}); err != nil {
		t.Fatalf("solve: %v", err)
	}
}

// counters renders every count of the exercise as "version/variant issued|solved" and "version/variant/task solved"
// keys. «solved» = teams that solved both subtasks of the variant (every seeded variant has 2), the same rule the
// use case applies to the solve depths.
func (s *vsSeed) counters(t *testing.T) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	issued, err := s.db.Queries.ListExerciseVariantIssues(ctx, s.exerciseID)
	if err != nil {
		t.Fatalf("list variant issues: %v", err)
	}
	tasks, err := s.db.Queries.ListExerciseVariantTaskSolves(ctx, s.exerciseID)
	if err != nil {
		t.Fatalf("list variant task solves: %v", err)
	}
	depths, err := s.db.Queries.ListExerciseVariantSolveDepths(ctx, s.exerciseID)
	if err != nil {
		t.Fatalf("list variant solve depths: %v", err)
	}
	out := map[string]int64{}
	for _, row := range issued {
		key := vsName(row.ExerciseVersionID) + "/" + vsName(row.VariantID)
		out[key+" issued"] = row.Issued
		out[key+" solved"] += 0
	}
	for _, row := range depths {
		if row.SolvedTasks >= 2 {
			out[vsName(row.ExerciseVersionID)+"/"+vsName(row.VariantID)+" solved"] += row.Teams
		}
	}
	for _, row := range tasks {
		out[vsName(row.ExerciseVersionID)+"/"+vsName(row.VariantID)+"/"+vsName(row.TaskID)+" solved"] = row.Solved
	}
	return out
}

func vsName(id uuid.UUID) string {
	switch id {
	case vsVersion1:
		return "v1"
	case vsVersion2:
		return "v2"
	case vsVariant0:
		return "var0"
	case vsVariant1:
		return "var1"
	case vsTask1:
		return "task1"
	case vsTask2:
		return "task2"
	}
	return id.String()
}

func assertCounters(t *testing.T, got, want map[string]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("counters = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("counters = %v, want %v (%s)", got, want, k)
		}
	}
}

// The counters after the base scenario of TestVariantStats_IssueAndSolveCounters.
func vsBaseCounters() map[string]int64 {
	return map[string]int64{
		"v1/var0 issued": 2, "v1/var0 solved": 1, "v1/var0/task1 solved": 2, "v1/var0/task2 solved": 1,
		"v1/var1 issued": 1, "v1/var1 solved": 0,
	}
}

// vsBaseScenario: T1 on variant 0 solves both, T2 on variant 0 solves task 1, T3 on variant 1
// solves none, the moderators team on variant 1 solves both.
func (s *vsSeed) baseScenario(t *testing.T) (t1, t2 [2]uuid.UUID) {
	t.Helper()
	t1 = s.assign(t, s.team(t, "one", false), 0)
	t2 = s.assign(t, s.team(t, "two", false), 0)
	s.assign(t, s.team(t, "three", false), 1)
	mods := s.assign(t, s.team(t, "mods", true), 1)
	s.solve(t, t1[0])
	s.solve(t, t1[1])
	s.solve(t, t2[0])
	s.solve(t, mods[0])
	s.solve(t, mods[1])
	return t1, t2
}

func TestVariantStats_IssueAndSolveCounters(t *testing.T) {
	s := seedVariantStats(t, "vsbase")
	s.baseScenario(t)
	assertCounters(t, s.counters(t), vsBaseCounters())
}

func TestVariantStats_GrowOnly(t *testing.T) {
	s := seedVariantStats(t, "vsgrow")
	ctx := context.Background()
	t1, _ := s.baseScenario(t)

	// A solve rejected by review deletes its projection row; the counters stay.
	if err := s.db.Queries.DeleteTeamChallengeSolve(ctx, t1[1]); err != nil {
		t.Fatal(err)
	}
	assertCounters(t, s.counters(t), vsBaseCounters())

	// Switching the event exercise to another version (same variant ids, as a copy keeps them):
	// the rewrite of the assignment counts the team once for the new version, the old counts stay.
	exercises := exerciseRepo.New(s.db.Queries)
	if _, err := exercises.UpsertDraft(ctx, s.exerciseID, vsVersion2, exerciseModel.ExerciseVersion{Variants: vsVariants()}, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := exercises.Publish(ctx, s.exerciseID, itNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Queries.UpdateEventExerciseSource(ctx, postgres.UpdateEventExerciseSourceParams{
		ID: s.eventExID, EventID: s.eventID, ExerciseID: s.exerciseID, ExerciseVersionID: vsVersion2,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range t1 {
		if _, err := s.db.Queries.UpdateTeamChallengeContent(ctx, postgres.UpdateTeamChallengeContentParams{
			ID: id, VariantIndex: 0, Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{x}", Now: itNow,
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := vsBaseCounters()
	want["v2/var0 issued"] = 1
	want["v2/var0 solved"] = 0
	assertCounters(t, s.counters(t), want)

	// Deleting the event keeps every counter.
	if _, err := s.db.Queries.DeleteEvent(ctx, s.eventID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	assertCounters(t, s.counters(t), want)
}

func TestVariantStats_NoDoubleCount(t *testing.T) {
	s := seedVariantStats(t, "vsdouble")
	ctx := context.Background()
	t1, t2 := s.baseScenario(t)

	// A projection rebuild deletes and reinserts the solve rows.
	if _, err := s.db.Pool.Exec(ctx, `DELETE FROM team_challenge_solves`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{t1[0], t1[1], t2[0]} {
		s.solve(t, id)
	}
	// Re-setting the same variant (a refresh) issues nothing new.
	for _, id := range append(t1[:], t2[:]...) {
		if _, err := s.db.Queries.UpdateTeamChallengeContent(ctx, postgres.UpdateTeamChallengeContentParams{
			ID: id, VariantIndex: 0, Snapshot: []byte(`{}`), Hints: []byte(`[]`), ExpectedFlag: "ICE{x}", Now: itNow,
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertCounters(t, s.counters(t), vsBaseCounters())
}

func TestVariantStats_BackfillIdempotent(t *testing.T) {
	s := seedVariantStats(t, "vsbackfill")
	ctx := context.Background()
	for _, table := range []string{"team_challenges", "team_challenge_solves"} {
		if _, err := s.db.Pool.Exec(ctx, `ALTER TABLE `+table+` DISABLE TRIGGER USER`); err != nil {
			t.Fatal(err)
		}
	}
	s.baseScenario(t)
	if got := s.counters(t); len(got) != 0 {
		t.Fatalf("triggers disabled, counters must be empty: %v", got)
	}
	for range 2 {
		if _, err := s.db.Pool.Exec(ctx, `SELECT exercise_variant_stats_backfill()`); err != nil {
			t.Fatalf("backfill: %v", err)
		}
		assertCounters(t, s.counters(t), vsBaseCounters())
	}
}

// Two subtasks of one variant solved in concurrent transactions still count the variant as solved (marks only, no
// lock: the count is aggregated when read).
func TestVariantStats_ConcurrentLastSubtask(t *testing.T) {
	s := seedVariantStats(t, "vsconcurrent")
	ctx := context.Background()
	rows := s.assign(t, s.team(t, "one", false), 0)

	first, err := s.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	if _, err = first.Exec(ctx, `INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $2)`, rows[0], itNow); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		second, bErr := s.db.Pool.Begin(ctx)
		if bErr != nil {
			done <- bErr
			return
		}
		if _, bErr = second.Exec(ctx, `INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $2)`, rows[1], itNow); bErr != nil {
			_ = second.Rollback(ctx)
			done <- bErr
			return
		}
		done <- second.Commit(ctx)
	}()
	// Let the second transaction reach the last-subtask check while the first is still open.
	if _, err = s.db.Pool.Exec(ctx, `SELECT pg_sleep(0.3)`); err != nil {
		t.Fatal(err)
	}
	if err = first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	assertCounters(t, s.counters(t), map[string]int64{
		"v1/var0 issued": 1, "v1/var0 solved": 1, "v1/var0/task1 solved": 1, "v1/var0/task2 solved": 1,
	})
}

// A statistics failure never fails the write it observes: legacy content (a non-uuid variant id, tasks as an object)
// counts nothing, and a mark insert that errors is only a warning.
func TestVariantStats_TriggerFailureNeverFailsTheWrite(t *testing.T) {
	s := seedVariantStats(t, "vsfailure")
	ctx := context.Background()
	if _, err := s.db.Pool.Exec(ctx, `UPDATE exercise_versions SET variants = jsonb_build_array(
		jsonb_build_object('id', 'not-a-uuid', 'tasks', jsonb_build_object('a', 1)),
		jsonb_build_object('id', 'also-not-a-uuid', 'tasks', '[]'::jsonb)) WHERE id = $1`, vsVersion1); err != nil {
		t.Fatal(err)
	}
	legacy := s.assign(t, s.team(t, "legacy", false), 0)
	s.solve(t, legacy[0])
	if got := s.counters(t); len(got) != 0 {
		t.Fatalf("legacy content must count nothing: %v", got)
	}
	// An index past the end (and a negative one would address from the end) resolves no variant.
	var resolved *string
	if err := s.db.Pool.QueryRow(ctx, `SELECT exercise_variant_stats_variant_id('[{"id":"0199f000-0000-7000-8000-0000000000a0"}]'::jsonb, -1)::text`).Scan(&resolved); err != nil || resolved != nil {
		t.Fatalf("a negative index must resolve nothing: %v %v", resolved, err)
	}

	// Restore valid content, then make every mark insert fail: the writes still succeed.
	exercises := exerciseRepo.New(s.db.Queries)
	if _, err := exercises.UpsertDraft(ctx, s.exerciseID, vsVersion2, exerciseModel.ExerciseVersion{Variants: vsVariants()}, itNow, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Pool.Exec(ctx, `UPDATE exercise_versions SET variants = (SELECT variants FROM exercise_versions WHERE id = $2) WHERE id = $1`, vsVersion1, vsVersion2); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"exercise_variant_issue_marks", "exercise_variant_solve_marks"} {
		if _, err := s.db.Pool.Exec(ctx, `ALTER TABLE `+table+` ADD CONSTRAINT broken CHECK (false) NOT VALID`); err != nil {
			t.Fatal(err)
		}
	}
	broken := s.assign(t, s.team(t, "broken", false), 0)
	s.solve(t, broken[0])
	s.solve(t, broken[1])
	if got := s.counters(t); len(got) != 0 {
		t.Fatalf("failed marks must count nothing: %v", got)
	}
}
