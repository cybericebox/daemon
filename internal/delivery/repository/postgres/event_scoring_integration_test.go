package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventScoringProfileRejectsInvalidPercent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	event := mustCreateEvent(t, eventRepo.New(db.Queries), "scoringprofile", "Scoring Profile", evNow, evNow.AddDate(0, 1, 0), [16]byte{}, evNow)

	_, err := db.Pool.Exec(context.Background(), `
		UPDATE events
		SET scoring_mode = 1,
		    dynamic_min_points = 100,
		    dynamic_max_points = 500,
		    dynamic_floor_at_percent = 101
		WHERE id = $1`, event.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
		t.Fatalf("want scoring profile check violation, got %v", err)
	}
}

func TestEventChallengeScoringProfileRejectsPartialOverride(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	event := mustCreateEvent(t, eventRepo.New(db.Queries), "scoreoverride", "Scoring Override", evNow, evNow.AddDate(0, 1, 0), uuid.Nil, evNow)
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)

	_, err := db.Pool.Exec(context.Background(), `
		UPDATE event_challenges
		SET scoring_mode = 1
		WHERE id = $1`, challenge.ID)
	assertCheckViolation(t, err)
}

func TestEventScoringPopulationRejectsNonPositiveCount(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	event := mustCreateEvent(t, eventRepo.New(db.Queries), "scorepopulation", "Scoring Population", evNow, evNow.AddDate(0, 1, 0), uuid.Nil, evNow)

	_, err := db.Pool.Exec(context.Background(), `
		INSERT INTO event_scoring_populations (event_id, units_count, captured_at)
		VALUES ($1, 0, $2)`, event.ID, evNow)
	assertCheckViolation(t, err)
}

func TestScoringPopulationQueriesAreGenerated(t *testing.T) {
	queryType := reflect.TypeOf(&postgres.Queries{})
	for _, method := range []string{"InsertEventScoringPopulation", "GetEventScoringPopulation"} {
		if _, ok := queryType.MethodByName(method); !ok {
			t.Fatalf("generated query API must expose %s", method)
		}
	}
}

func TestEventScoringPopulationInsertKeepsFirstSnapshot(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustCreateEvent(t, eventRepo.New(db.Queries), "scorefirst", "Scoring First", evNow, evNow.AddDate(0, 1, 0), uuid.Nil, evNow)

	first, err := db.Queries.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{
		EventID: event.ID, UnitsCount: 12, CapturedAt: evNow,
	})
	if err != nil {
		t.Fatalf("capture first population: %v", err)
	}
	second, err := db.Queries.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{
		EventID: event.ID, UnitsCount: 99, CapturedAt: evNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("repeat population capture: %v", err)
	}
	if second != first {
		t.Fatalf("repeat must return original snapshot: got %+v, want %+v", second, first)
	}
	got, err := db.Queries.GetEventScoringPopulation(ctx, event.ID)
	if err != nil || got != first {
		t.Fatalf("stored snapshot: got %+v err=%v, want %+v", got, err, first)
	}
}

func TestGetEventContentStatisticsCountsChallengesAndAcceptedSolves(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "contentstats")
	published := mustCreateScoringChallenge(t, db.Queries, event.ID)
	if _, err := db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventExerciseID: published.EventExerciseID, TaskID: uuid.Must(uuid.NewV7()),
		OrderIndex: 1, Points: 100, Snapshot: []byte(`{}`), CreatedAt: itNow,
	}); err != nil {
		t.Fatalf("create draft challenge: %v", err)
	}
	if _, err := db.Queries.UpdateEventChallenge(ctx, postgres.UpdateEventChallengeParams{
		ID: published.ID, EventExerciseID: published.EventExerciseID, Points: published.Points,
		HintsEnabled: published.HintsEnabled, Published: true,
	}); err != nil {
		t.Fatalf("publish challenge: %v", err)
	}

	teams := eventTeamRepo.New(db.Queries)
	for _, suffix := range []string{"one", "two"} {
		captainID := mustSeedUser(t, db, "contentstats-"+suffix+"@test.test")
		team, err := eventTeamModel.New(event.ID, captainID, "Team "+suffix, "join-code-"+suffix, itNow)
		if err != nil {
			t.Fatalf("new team: %v", err)
		}
		team, err = teams.Create(ctx, team)
		if err != nil {
			t.Fatalf("create team: %v", err)
		}
		teamChallenge, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID,
			EventChallengeID: published.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
		})
		if err != nil {
			t.Fatalf("create team challenge: %v", err)
		}
		if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{
			TeamChallengeID: teamChallenge.ID, SolvedAt: itNow, AwardedPoints: pgtype.Int4{Int32: 100, Valid: true},
		}); err != nil {
			t.Fatalf("store solve: %v", err)
		}
	}

	stats, err := db.Queries.GetEventContentStatistics(ctx, postgres.GetEventContentStatisticsParams{EventID: event.ID})
	if err != nil {
		t.Fatalf("get content statistics: %v", err)
	}
	if stats.ChallengeCount != 2 || stats.PublishedChallengeCount != 1 || stats.SolvedChallengeCount != 1 || stats.SolveCount != 2 {
		t.Fatalf("unexpected content statistics: %+v", stats)
	}
}

func TestHiddenTeamSolvesDoNotAffectVisibleScoring(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "hiddenscoring")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET scoring_mode = 1, dynamic_min_points = 100,
		dynamic_max_points = 500, dynamic_floor_at_percent = 100 WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{
		EventID: event.ID, UnitsCount: 3, CapturedAt: itNow,
	}); err != nil {
		t.Fatal(err)
	}
	otherCaptainID := mustSeedUser(t, db, "hidden-scoring-other@test.test")
	otherTeam, err := eventTeamModel.New(event.ID, otherCaptainID, "other", "hidden-code-other", itNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, otherTeam); err != nil {
		t.Fatal(err)
	}
	var visibleID, hiddenID uuid.UUID
	for index, suffix := range []string{"hidden", "visible"} {
		captainID := mustSeedUser(t, db, "hidden-scoring-"+suffix+"@test.test")
		team, err := eventTeamModel.New(event.ID, captainID, suffix, "hidden-code-"+suffix, itNow)
		if err != nil {
			t.Fatal(err)
		}
		team, err = eventTeamRepo.New(db.Queries).Create(ctx, team)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			hiddenID = team.ID
			if _, err = db.Pool.Exec(ctx, `UPDATE event_teams SET hidden = true WHERE id = $1`, team.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			visibleID = team.ID
		}
		teamChallenge, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID,
			EventChallengeID: challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{
			TeamChallengeID: teamChallenge.ID, SolvedAt: itNow.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	visible, err := db.Queries.ListTeamScoreTimeline(ctx, visibleID)
	if err != nil || len(visible) != 1 || visible[0].Points != 300 {
		t.Fatalf("hidden team must be absent from score population: rows=%+v err=%v", visible, err)
	}
	hidden, err := db.Queries.ListTeamScoreTimeline(ctx, hiddenID)
	if err != nil || len(hidden) != 1 || hidden[0].Points != 100 {
		t.Fatalf("hidden team must receive fixed challenge points: rows=%+v err=%v", hidden, err)
	}
	public, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event.ID})
	if err != nil || len(public) != 2 || public[0].TeamID != visibleID {
		t.Fatalf("hidden team must be absent from ranking: rows=%+v err=%v", public, err)
	}
	// An unpublished task is no public news, whoever solved it.
	stats, err := db.Queries.GetEventContentStatistics(ctx, postgres.GetEventContentStatisticsParams{EventID: event.ID})
	if err != nil || stats.SolveCount != 0 || stats.SolvedChallengeCount != 0 {
		t.Fatalf("an unpublished task must not count: stats=%+v err=%v", stats, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE event_challenges SET published = true`); err != nil {
		t.Fatal(err)
	}
	stats, err = db.Queries.GetEventContentStatistics(ctx, postgres.GetEventContentStatisticsParams{EventID: event.ID})
	if err != nil || stats.SolveCount != 1 {
		t.Fatalf("hidden solve must be absent from public stats: stats=%+v err=%v", stats, err)
	}
	// While the results are frozen only the solves before the freeze count.
	before := postgres.GetEventContentStatisticsParams{EventID: event.ID, Cutoff: pgtype.Timestamptz{Time: itNow.Add(-time.Hour), Valid: true}}
	if stats, err = db.Queries.GetEventContentStatistics(ctx, before); err != nil || stats.SolveCount != 0 {
		t.Fatalf("a solve after the freeze must not count: stats=%+v err=%v", stats, err)
	}
	after := postgres.GetEventContentStatisticsParams{EventID: event.ID, Cutoff: pgtype.Timestamptz{Time: itNow.Add(time.Hour), Valid: true}}
	if stats, err = db.Queries.GetEventContentStatistics(ctx, after); err != nil || stats.SolveCount != 1 {
		t.Fatalf("a solve before the freeze counts: stats=%+v err=%v", stats, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE events SET scoring_mode = 2 WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE event_teams SET hidden = false WHERE id = $1`, hiddenID); err != nil {
		t.Fatal(err)
	}
	visible, err = db.Queries.ListTeamScoreTimeline(ctx, visibleID)
	if err != nil || len(visible) != 1 || visible[0].Points != 300 {
		t.Fatalf("second visible solve must use second-place ladder points: rows=%+v err=%v", visible, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE event_teams SET hidden = true WHERE id = $1`, hiddenID); err != nil {
		t.Fatal(err)
	}
	visible, err = db.Queries.ListTeamScoreTimeline(ctx, visibleID)
	if err != nil || len(visible) != 1 || visible[0].Points != 500 {
		t.Fatalf("hiding earlier solve must restore first-place points: rows=%+v err=%v", visible, err)
	}
}

func mustCreateScoringChallenge(t *testing.T, q *postgres.Queries, eventID uuid.UUID) postgres.EventChallenge {
	t.Helper()
	ctx := context.Background()
	exercises := exerciseRepo.New(q)
	exercise := mustCreateExercise(t, exercises, "Scoring challenge")
	draft, err := exercises.UpsertDraft(ctx, exercise.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("save exercise draft: %v", err)
	}
	published, err := exercises.Publish(ctx, exercise.ID, itNow)
	if err != nil {
		t.Fatalf("publish exercise: %v", err)
	}
	eventExercise, err := q.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: exercise.ID, ExerciseVersionID: draft.ID,
		CreatedAt: itNow,
	})
	if err != nil {
		t.Fatalf("attach exercise: %v", err)
	}
	challenge, err := q.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventExerciseID: eventExercise.ID, TaskID: published.Variants[0].Tasks[0].ID,
		Points: 100, Snapshot: []byte(`{}`), CreatedAt: itNow,
	})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	return challenge
}

func assertCheckViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
		t.Fatalf("want scoring profile check violation, got %v", err)
	}
}
