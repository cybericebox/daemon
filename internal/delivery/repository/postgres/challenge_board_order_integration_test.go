package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// boardSet attaches one more exercise (a «набір») to the event.
func boardSet(t *testing.T, q *postgres.Queries, eventID uuid.UUID, name string, createdAt time.Time) postgres.EventExercise {
	t.Helper()
	ctx := context.Background()
	exercises := exerciseRepo.New(q)
	exercise := mustCreateExercise(t, exercises, name)
	draft, err := exercises.UpsertDraft(ctx, exercise.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("save exercise draft: %v", err)
	}
	link, err := q.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: exercise.ID, ExerciseVersionID: draft.ID, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("attach exercise: %v", err)
	}
	return link
}

func boardChallenge(t *testing.T, q *postgres.Queries, link postgres.EventExercise, order int32, name string, points int32) postgres.EventChallenge {
	t.Helper()
	challenge, err := q.CreateEventChallenge(context.Background(), postgres.CreateEventChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventExerciseID: link.ID, TaskID: uuid.Must(uuid.NewV7()), OrderIndex: order,
		Points: points, Published: true, Snapshot: []byte(`{"name":"` + name + `"}`), CreatedAt: itNow,
	})
	if err != nil {
		t.Fatalf("create challenge %s: %v", name, err)
	}
	return challenge
}

func boardNames(t *testing.T, q *postgres.Queries, teamID uuid.UUID) ([]string, []int32, []int32) {
	t.Helper()
	rows, err := q.ListTeamBoardChallenges(context.Background(), postgres.ListTeamBoardChallengesParams{EventTeamID: teamID, PublishedOnly: true})
	if err != nil {
		t.Fatalf("list board: %v", err)
	}
	names, positions, points := make([]string, 0, len(rows)), make([]int32, 0, len(rows)), make([]int32, 0, len(rows))
	for _, row := range rows {
		names = append(names, string(row.Snapshot))
		positions = append(positions, row.BoardPosition)
		points = append(points, row.Points)
	}
	return names, positions, points
}

// The group order set on the manage page crosses sets and is what the
// participant board reads; a move to another group goes to its end.
func TestChallengeBoardOrderInsideGroupAcrossSets(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	event := mustSeedEventForParticipants(t, db, "boardorder")
	first := boardSet(t, q, event.ID, "Board set one", itNow)
	second := boardSet(t, q, event.ID, "Board set two", itNow.Add(time.Minute))
	a := boardChallenge(t, q, first, 0, "a", 100)
	b := boardChallenge(t, q, first, 1, "b", 100)
	c := boardChallenge(t, q, second, 0, "c", 100)
	group, err := q.CreateEventChallengeGroup(ctx, postgres.CreateEventChallengeGroupParams{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Name: "Web", OrderIndex: 0, CreatedAt: itNow})
	if err != nil {
		t.Fatal(err)
	}
	web := uuid.NullUUID{UUID: group.ID, Valid: true}
	for _, challenge := range []postgres.EventChallenge{a, b, c} {
		if _, err = q.SetEventChallengeGroup(ctx, postgres.SetEventChallengeGroupParams{GroupID: web, ID: challenge.ID, EventExerciseID: challenge.EventExerciseID}); err != nil {
			t.Fatal(err)
		}
	}

	captainID := mustSeedUser(t, db, "boardorder-captain@test.test")
	team, err := eventTeamModel.New(event.ID, captainID, "Order team", "board-order-code", itNow)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(q).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	for _, challenge := range []postgres.EventChallenge{c, b, a} {
		if _, err = q.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID, EventChallengeID: challenge.ID,
			Snapshot: challenge.Snapshot, ExpectedFlag: "ICE{x}", Readiness: 2, CreatedAt: itNow,
		}); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := q.ListEventGroupChallengeIDs(ctx, postgres.ListEventGroupChallengeIDsParams{EventID: event.ID, GroupID: web})
	if err != nil || len(ids) != 3 {
		t.Fatalf("group challenges: %v err=%v", ids, err)
	}
	// Unordered: by set attach time, then set order.
	names, positions, _ := boardNames(t, q, team.ID)
	if want := []string{`{"name": "a"}`, `{"name": "b"}`, `{"name": "c"}`}; !equalStrings(names, want) || !equalInts(positions, []int32{0, 1, 2}) {
		t.Fatalf("default board order = %v %v", names, positions)
	}

	for index, challenge := range []postgres.EventChallenge{c, a, b} {
		affected, setErr := q.SetEventChallengeBoardOrder(ctx, postgres.SetEventChallengeBoardOrderParams{BoardOrder: pgtype.Int4{Int32: int32(index), Valid: true}, ID: challenge.ID, EventID: event.ID})
		if setErr != nil || affected != 1 {
			t.Fatalf("set board order: affected=%d err=%v", affected, setErr)
		}
	}
	// Another event cannot reorder this event's challenges.
	if affected, _ := q.SetEventChallengeBoardOrder(ctx, postgres.SetEventChallengeBoardOrderParams{BoardOrder: pgtype.Int4{Int32: 9, Valid: true}, ID: a.ID, EventID: uuid.Must(uuid.NewV7())}); affected != 0 {
		t.Fatal("board order must be scoped to the event")
	}
	names, _, _ = boardNames(t, q, team.ID)
	if want := []string{`{"name": "c"}`, `{"name": "a"}`, `{"name": "b"}`}; !equalStrings(names, want) {
		t.Fatalf("group board order = %v", names)
	}

	// Staying in the group keeps the position; leaving it clears it.
	if _, err = q.SetEventChallengeGroup(ctx, postgres.SetEventChallengeGroupParams{GroupID: web, ID: c.ID, EventExerciseID: c.EventExerciseID}); err != nil {
		t.Fatal(err)
	}
	var kept pgtype.Int4
	if err = db.Pool.QueryRow(ctx, `SELECT board_order FROM event_challenges WHERE id = $1`, c.ID).Scan(&kept); err != nil || !kept.Valid || kept.Int32 != 0 {
		t.Fatalf("same group must keep the position: %+v err=%v", kept, err)
	}
	if _, err = q.SetEventChallengeGroup(ctx, postgres.SetEventChallengeGroupParams{ID: c.ID, EventExerciseID: c.EventExerciseID}); err != nil {
		t.Fatal(err)
	}
	var cleared pgtype.Int4
	if err = db.Pool.QueryRow(ctx, `SELECT board_order FROM event_challenges WHERE id = $1`, c.ID).Scan(&cleared); err != nil || cleared.Valid {
		t.Fatalf("a move to another group must clear the position: %+v err=%v", cleared, err)
	}
	names, positions, _ = boardNames(t, q, team.ID)
	if want := []string{`{"name": "a"}`, `{"name": "b"}`, `{"name": "c"}`}; !equalStrings(names, want) || !equalInts(positions, []int32{0, 1, 0}) {
		t.Fatalf("board after the move = %v %v", names, positions)
	}
}

// Static event scoring with one value applies to tasks that follow the event
// (and to every task when forced); own static tasks keep their points.
func TestEventStaticPointsApplyToFollowingTasks(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	event := mustSeedEventForParticipants(t, db, "staticpoints")
	link := boardSet(t, q, event.ID, "Static set", itNow)
	following := boardChallenge(t, q, link, 0, "following", 100)
	own := boardChallenge(t, q, link, 1, "own", 250)
	if _, err := db.Pool.Exec(ctx, `UPDATE event_challenges SET scoring_mode = 0 WHERE id = $1`, own.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET static_points = 0 WHERE id = $1`, event.ID); err == nil {
		t.Fatal("static points must be positive")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET static_points = 40 WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	captainID := mustSeedUser(t, db, "staticpoints-captain@test.test")
	team, err := eventTeamModel.New(event.ID, captainID, "Static team", "static-points-code", itNow)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(q).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	for _, challenge := range []postgres.EventChallenge{following, own} {
		teamChallenge, createErr := q.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID, EventChallengeID: challenge.ID,
			Snapshot: challenge.Snapshot, ExpectedFlag: "ICE{x}", Readiness: 2, CreatedAt: itNow,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if err = q.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallenge.ID, SolvedAt: itNow}); err != nil {
			t.Fatal(err)
		}
		scoring, scoringErr := q.GetTeamChallengeScoringContext(ctx, teamChallenge.ID)
		if scoringErr != nil {
			t.Fatal(scoringErr)
		}
		want := int32(40)
		if challenge.ID == own.ID {
			want = 250
		}
		if scoring.StaticPoints != want {
			t.Fatalf("scoring context static points for %s = %d, want %d", challenge.Snapshot, scoring.StaticPoints, want)
		}
	}
	_, _, points := boardNames(t, q, team.ID)
	if !equalInts(points, []int32{40, 250}) {
		t.Fatalf("board points = %v", points)
	}
	timeline, err := q.ListTeamScoreTimeline(ctx, team.ID)
	if err != nil || len(timeline) != 2 || timeline[0].Points+timeline[1].Points != 290 {
		t.Fatalf("scores: %+v err=%v", timeline, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET force_event_scoring = true WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	timeline, err = q.ListTeamScoreTimeline(ctx, team.ID)
	if err != nil || len(timeline) != 2 || timeline[0].Points+timeline[1].Points != 80 {
		t.Fatalf("forced scores: %+v err=%v", timeline, err)
	}
}

// 0103: static scoring requires its value. An event without one gets the
// most common points of its following tasks; a following task with other
// points keeps them as its own static scoring, so scores do not change.
func TestStaticPointsRequiredMigrationKeepsScores(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	event := mustSeedEventForParticipants(t, db, "staticmigration")
	link := boardSet(t, q, event.ID, "Migration set", itNow)
	common1 := boardChallenge(t, q, link, 0, "one", 200)
	common2 := boardChallenge(t, q, link, 1, "two", 200)
	other := boardChallenge(t, q, link, 2, "other", 50)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET static_points = NULL WHERE id = $1`, event.ID); err == nil {
		t.Fatal("static scoring without static points must be rejected")
	}
	empty := mustSeedEventForParticipants(t, db, "staticmigrationempty")
	up, err := os.ReadFile("migrations/0103_event_static_points_required.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE events DROP CONSTRAINT events_static_points_required_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET static_points = NULL WHERE id IN ($1, $2)`, event.ID, empty.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-run 0103: %v", err)
	}
	var points, emptyPoints int32
	if err = db.Pool.QueryRow(ctx, `SELECT static_points FROM events WHERE id = $1`, event.ID).Scan(&points); err != nil || points != 200 {
		t.Fatalf("most common points = %d err=%v", points, err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT static_points FROM events WHERE id = $1`, empty.ID).Scan(&emptyPoints); err != nil || emptyPoints != 100 {
		t.Fatalf("default points = %d err=%v", emptyPoints, err)
	}
	for _, check := range []struct {
		challenge postgres.EventChallenge
		own       bool
	}{{common1, false}, {common2, false}, {other, true}} {
		var mode pgtype.Int2
		if err = db.Pool.QueryRow(ctx, `SELECT scoring_mode FROM event_challenges WHERE id = $1`, check.challenge.ID).Scan(&mode); err != nil || mode.Valid != check.own {
			t.Fatalf("%s own static = %v err=%v", check.challenge.Snapshot, mode, err)
		}
	}
}

// 0108: a set is all-or-nothing. Tasks removed one by one come back from
// the pinned version, and a set is shown when any of its tasks was shown.
func TestSetAllOrNothingMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	event := mustSeedEventForParticipants(t, db, "allornothing")
	removed := boardSet(t, q, event.ID, "Removed task set", itNow)
	mixed := boardSet(t, q, event.ID, "Mixed visibility set", itNow)
	boardChallenge(t, q, mixed, 0, "shown", 100)
	hidden := boardChallenge(t, q, mixed, 1, "hidden", 100)
	if _, err := db.Pool.Exec(ctx, `UPDATE event_challenges SET published = false WHERE id = $1`, hidden.ID); err != nil {
		t.Fatal(err)
	}
	var taskID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `SELECT (v.variants->0->'tasks'->0->>'id')::uuid FROM exercise_versions v WHERE v.id = $1`, removed.ExerciseVersionID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("migrations/0108_set_all_or_nothing.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE event_exercises ADD COLUMN excluded_task_ids uuid[] NOT NULL DEFAULT '{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE event_exercises SET excluded_task_ids = ARRAY[$2::uuid] WHERE id = $1`, removed.ID, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-run 0108: %v", err)
	}
	restored, err := q.ListEventChallenges(ctx, removed.ID)
	if err != nil || len(restored) != 1 || restored[0].TaskID != taskID || restored[0].Points != 100 || restored[0].Published {
		t.Fatalf("restored task = %+v err=%v", restored, err)
	}
	if !strings.Contains(string(restored[0].Snapshot), `"name": "Find the flag"`) || !strings.Contains(string(restored[0].Snapshot), `"difficulty": "easy"`) {
		t.Fatalf("restored snapshot = %s", restored[0].Snapshot)
	}
	board, err := q.ListEventChallenges(ctx, mixed.ID)
	if err != nil || len(board) != 2 || !board[0].Published || !board[1].Published {
		t.Fatalf("a set with a shown task must be shown whole: %+v err=%v", board, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
