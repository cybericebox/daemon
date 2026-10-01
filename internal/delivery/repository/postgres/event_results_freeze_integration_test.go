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

// TestScoresAtCutoffFreezeDynamicPoints covers W8: a frozen table computes
// popularity points as of the cutoff (later solves do not leak through
// decay), the own team is read with its later solves, and the view keeps
// returning the live scores through the same formula.
func TestScoresAtCutoffFreezeDynamicPoints(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "resultsfreeze")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET scoring_mode = 1, dynamic_min_points = 100,
		dynamic_max_points = 500, dynamic_floor_at_percent = 100 WHERE id = $1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Queries.InsertEventScoringPopulation(ctx, postgres.InsertEventScoringPopulationParams{EventID: event.ID, UnitsCount: 3, CapturedAt: itNow}); err != nil {
		t.Fatal(err)
	}
	teams := make([]uuid.UUID, 0, 3)
	teamChallenges := make([]uuid.UUID, 0, 3)
	for index, suffix := range []string{"a", "b", "own"} {
		captainID := mustSeedUser(t, db, "results-freeze-"+suffix+"@test.test")
		team, err := eventTeamModel.New(event.ID, captainID, "team "+suffix, "freeze-code-"+suffix, itNow)
		if err != nil {
			t.Fatal(err)
		}
		if team, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
			t.Fatal(err)
		}
		teamChallenge, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID,
			EventChallengeID: challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{
			TeamChallengeID: teamChallenge.ID, SolvedAt: itNow.Add(time.Duration(index*10) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		teams = append(teams, team.ID)
		teamChallenges = append(teamChallenges, teamChallenge.ID)
	}
	a, own := teams[0], teams[2]
	cutoff := pgtype.Timestamptz{Time: itNow.Add(5 * time.Minute), Valid: true}

	frozen, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event.ID, Cutoff: cutoff})
	if err != nil || len(frozen) != 3 || frozen[0].TeamID != a || frozen[0].Points != 396 || frozen[0].Solved != 1 || frozen[1].Solved != 0 {
		t.Fatalf("frozen table must hold only the first solve at its early points: rows=%+v err=%v", frozen, err)
	}
	withOwn, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event.ID, Cutoff: cutoff, IncludeTeam: uuid.NullUUID{UUID: own, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range withOwn {
		if row.TeamID == own && (row.Points != 204 || row.Solved != 1) {
			t.Fatalf("own team must see its later solve: %+v", row)
		}
	}
	live, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event.ID})
	if err != nil || len(live) != 3 || live[0].Points != 100 || live[2].Points != 100 {
		t.Fatalf("live table: rows=%+v err=%v", live, err)
	}
	timeline, err := db.Queries.ListTeamScoreTimeline(ctx, a)
	if err != nil || len(timeline) != 1 || timeline[0].Points != 100 {
		t.Fatalf("the view must keep the live formula: rows=%+v err=%v", timeline, err)
	}
	frozenTimeline, err := db.Queries.ListEventScoreTimeline(ctx, postgres.ListEventScoreTimelineParams{EventID: event.ID, Cutoff: cutoff})
	if err != nil || len(frozenTimeline) != 1 || frozenTimeline[0].EventTeamID != a {
		t.Fatalf("frozen timeline: rows=%+v err=%v", frozenTimeline, err)
	}
	counts, err := db.Queries.CountEventChallengeSolves(ctx, postgres.CountEventChallengeSolvesParams{EventID: event.ID, OwnTeamID: own, Cutoff: cutoff})
	if err != nil || len(counts) != 1 || counts[0].Solves != 2 {
		t.Fatalf("frozen solve count = other teams before the cutoff + own: %+v err=%v", counts, err)
	}
	solves, err := db.Queries.ListEventChallengeSolves(ctx, postgres.ListEventChallengeSolvesParams{EventID: event.ID, EventChallengeID: challenge.ID, OwnTeamID: own, Cutoff: cutoff, RowLimit: 10})
	if err != nil || len(solves) != 2 || solves[0].EventTeamID != a || solves[1].EventTeamID != own {
		t.Fatalf("frozen solves list: %+v err=%v", solves, err)
	}

	if _, err = db.Pool.Exec(ctx, `UPDATE event_teams SET hidden = true WHERE id = $1`, teams[1]); err != nil {
		t.Fatal(err)
	}
	manage, err := db.Queries.ListManageScoreboard(ctx, event.ID)
	if err != nil || len(manage) != 3 {
		t.Fatalf("moderators see every team: rows=%+v err=%v", manage, err)
	}
	hiddenListed := false
	for _, row := range manage {
		if row.TeamID == teams[1] {
			hiddenListed = row.Hidden && row.RealName == "team b" && row.Pseudonym == ""
		}
	}
	if !hiddenListed {
		t.Fatalf("hidden team must be listed with its mark: %+v", manage)
	}
	counted := map[uuid.UUID]bool{}
	for _, row := range manage {
		counted[row.TeamID] = !row.Hidden && row.Admitted
	}
	manageSolves, err := db.Queries.ListManageScoreSolves(ctx, event.ID)
	if err != nil || len(manageSolves) != 3 || manageSolves[0].EventChallengeID != challenge.ID {
		t.Fatalf("moderators see every solve: rows=%+v err=%v", manageSolves, err)
	}
	firstBloods := 0
	for index, row := range manageSolves {
		if !row.FirstBlood {
			continue
		}
		firstBloods++
		if !counted[row.EventTeamID] {
			t.Fatalf("first blood goes to a ranked team only: %+v", row)
		}
		for _, earlier := range manageSolves[:index] {
			if counted[earlier.EventTeamID] {
				t.Fatalf("first blood is the earliest ranked solve: %+v", manageSolves)
			}
		}
	}
	if firstBloods > 1 || (firstBloods == 0 && (counted[teams[0]] || counted[teams[2]])) {
		t.Fatalf("one first blood per challenge: %+v", manageSolves)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO team_challenge_hint_unlocks (team_challenge_id, hint_id, event_id, event_team_id, event_challenge_id, unlocked_at, cost)
		VALUES ($1, $2, $3, $4, $5, $6, 30), ($1, $7, $3, $4, $5, $8, 20)`,
		teamChallenges[2], uuid.Must(uuid.NewV7()), event.ID, own, challenge.ID, itNow.Add(10*time.Minute), uuid.Must(uuid.NewV7()), itNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hints, err := db.Queries.ListManageHintTotals(ctx, event.ID)
	if err != nil || len(hints) != 1 || hints[0].EventTeamID != own || hints[0].Hints != 2 || hints[0].Charged != 30 {
		t.Fatalf("hints are charged only before the solve: rows=%+v err=%v", hints, err)
	}

	locked, err := db.Queries.LockEventTeamChallenge(ctx, postgres.LockEventTeamChallengeParams{EventID: event.ID, EventTeamID: a, EventChallengeID: challenge.ID})
	if err != nil || locked != teamChallenges[0] {
		t.Fatalf("lock team challenge = %v, %v", locked, err)
	}
	stamp, err := db.Queries.GetEventAttemptsStamp(ctx, event.ID)
	if err != nil || stamp.Attempts != 0 || stamp.Decisions != 0 {
		t.Fatalf("attempts stamp = %+v, %v", stamp, err)
	}
}
