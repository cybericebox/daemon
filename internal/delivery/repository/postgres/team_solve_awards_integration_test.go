package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

type awardSeed struct {
	team          uuid.UUID
	teamChallenge uuid.UUID
	solver        uuid.UUID
}

// seedAwardTeam gives a team the task, a wrong answer from its captain and then a correct one from a second member.
func seedAwardTeam(t *testing.T, db *testhelpers.TestDB, eventID, challengeID uuid.UUID, tag string, practice bool, solvedAt time.Time) awardSeed {
	t.Helper()
	ctx := context.Background()
	captainID := mustSeedUser(t, db, "award-"+tag+"-captain@test.test")
	memberID := mustSeedUser(t, db, "award-"+tag+"-member@test.test")
	team, err := eventTeamModel.New(eventID, captainID, "award "+tag, "award-code-"+tag, itNow)
	if err != nil {
		t.Fatal(err)
	}
	team, err = eventTeamRepo.New(db.Queries).Create(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	teamChallenge, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: team.ID,
		EventChallengeID: challengeID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		user    uuid.UUID
		correct bool
		at      time.Time
	}{{captainID, false, solvedAt.Add(-time.Minute)}, {memberID, true, solvedAt}} {
		if _, err = db.Queries.CreateChallengeAttempt(ctx, postgres.CreateChallengeAttemptParams{
			ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: team.ID, TeamChallengeID: teamChallenge.ID,
			UserID: attempt.user, Answer: "a", Correct: attempt.correct, ReceivedAt: attempt.at, CreatedAt: attempt.at, Practice: practice,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !practice {
		if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallenge.ID, SolvedAt: solvedAt}); err != nil {
			t.Fatal(err)
		}
	}
	return awardSeed{team: team.ID, teamChallenge: teamChallenge.ID, solver: memberID}
}

func awardOf(t *testing.T, q *postgres.Queries, seed awardSeed) postgres.ListTeamSolveAwardsRow {
	t.Helper()
	rows, err := q.ListTeamSolveAwards(context.Background(), seed.team)
	if err != nil || len(rows) != 1 {
		t.Fatalf("awards: rows=%+v err=%v", rows, err)
	}
	if rows[0].TeamChallengeID != seed.teamChallenge || rows[0].SolverID != seed.solver {
		t.Fatalf("the accepted answer's author must be the solver: %+v", rows[0])
	}
	return rows[0]
}

func TestTeamSolveAwardsStaticAndHintPenalty(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "awardstatic")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	plain := seedAwardTeam(t, db, event.ID, challenge.ID, "plain", false, itNow)
	hinted := seedAwardTeam(t, db, event.ID, challenge.ID, "hinted", false, itNow.Add(time.Hour))

	if award := awardOf(t, db.Queries, plain); award.AwardedPoints != 100 || award.HintPenalty != 0 {
		t.Fatalf("static solve: %+v", award)
	}
	for i, at := range []time.Time{itNow.Add(time.Minute), itNow.Add(2 * time.Hour)} {
		if _, err := db.Queries.CreateHintUnlock(ctx, postgres.CreateHintUnlockParams{
			TeamChallengeID: hinted.teamChallenge, HintID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: hinted.team,
			EventChallengeID: challenge.ID, UnlockedAt: at, Cost: int32(10 * (i + 1)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Without the balance mode only a hint opened before the solve is charged from the reward.
	if award := awardOf(t, db.Queries, hinted); award.AwardedPoints != 90 || award.HintPenalty != 10 {
		t.Fatalf("hint penalty: %+v", award)
	}
}

func TestTeamSolveAwardsBalanceModeChargesEveryHint(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "awardbalance")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	setHintChargeMode(t, db, event.ID, 1)
	seed := seedAwardTeam(t, db, event.ID, challenge.ID, "balance", false, itNow)
	if _, err := db.Queries.CreateHintUnlock(ctx, postgres.CreateHintUnlockParams{
		TeamChallengeID: seed.teamChallenge, HintID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: seed.team,
		EventChallengeID: challenge.ID, UnlockedAt: itNow.Add(time.Hour), Cost: 25,
	}); err != nil {
		t.Fatal(err)
	}
	if award := awardOf(t, db.Queries, seed); award.AwardedPoints != 75 || award.HintPenalty != 25 {
		t.Fatalf("balance penalty: %+v", award)
	}
}

func TestTeamSolveAwardsDynamicUsesCurrentValue(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "awarddynamic")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET scoring_mode = 1, dynamic_min_points = 100,
		dynamic_max_points = 500, dynamic_floor_at_percent = 100 WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{EventID: event.ID, UnitsCount: 2, CapturedAt: itNow}); err != nil {
		t.Fatal(err)
	}
	first := seedAwardTeam(t, db, event.ID, challenge.ID, "first", false, itNow)
	// The second team exists but has not solved it yet: half of the population solved it.
	laterCaptain := mustSeedUser(t, db, "award-later@test.test")
	later, err := eventTeamModel.New(event.ID, laterCaptain, "award later", "award-code-later", itNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, later); err != nil {
		t.Fatal(err)
	}
	if award := awardOf(t, db.Queries, first); award.AwardedPoints != 300 {
		t.Fatalf("half of the teams solved it: %+v", award)
	}
	_, err = db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: later.ID,
		EventChallengeID: challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	laterChallenge, err := db.Queries.GetTeamChallenge(ctx, postgres.GetTeamChallengeParams{EventTeamID: later.ID, EventChallengeID: challenge.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: laterChallenge.ID, SolvedAt: itNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Every visible team solved it, so the value fell to the floor for both.
	if award := awardOf(t, db.Queries, first); award.AwardedPoints != 100 {
		t.Fatalf("the award follows the recomputed value: %+v", award)
	}
}

func TestTeamSolveAwardsPracticeSolveIsWorthNothing(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	event := mustSeedEventForParticipants(t, db, "awardpractice")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	seed := seedAwardTeam(t, db, event.ID, challenge.ID, "practice", true, itNow)
	if award := awardOf(t, db.Queries, seed); award.AwardedPoints != 0 || award.HintPenalty != 0 {
		t.Fatalf("practice solve: %+v", award)
	}
}

func TestTeamSolveAwardsSkipUnsolvedAndOtherTeams(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "awardscope")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	mine := seedAwardTeam(t, db, event.ID, challenge.ID, "mine", false, itNow)
	other := seedAwardTeam(t, db, event.ID, challenge.ID, "other", false, itNow)
	if _, err := db.Pool.Exec(ctx, `DELETE FROM team_challenge_solves WHERE team_challenge_id = $1`, mine.teamChallenge); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE challenge_attempts SET correct = false WHERE team_challenge_id = $1`, mine.teamChallenge); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Queries.ListTeamSolveAwards(ctx, mine.team)
	if err != nil || len(rows) != 0 {
		t.Fatalf("an unsolved task has no award and another team's solve never shows: rows=%+v err=%v", rows, err)
	}
	if awardOf(t, db.Queries, other).SolverID != other.solver {
		t.Fatal("the other team keeps its own solver")
	}
	_ = pgtype.Int4{}
}
