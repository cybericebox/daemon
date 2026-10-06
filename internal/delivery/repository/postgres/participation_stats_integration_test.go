package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The participation reads are scoped to one team: its solves, the solver, the
// first blood (only for the team that really solved first), and the counters
// of the members; another team's rows never come back.
func TestParticipationStatsAreScopedToOneTeam(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "participationstats")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	type seeded struct {
		team          uuid.UUID
		teamChallenge uuid.UUID
		captain       uuid.UUID
	}
	seed := func(suffix string, solvedAt time.Time) seeded {
		captainID := mustSeedUser(t, db, "participation-"+suffix+"@test.test")
		team, err := eventTeamModel.New(event.ID, captainID, "team "+suffix, "participation-code-"+suffix, itNow)
		if err != nil {
			t.Fatal(err)
		}
		if team, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(ctx, `INSERT INTO event_participants (event_id, user_id, status, created_at, decided_at, team_id, team_role)
			VALUES ($1, $2, 2, $3, $3, $4, 0) ON CONFLICT (event_id, user_id) DO UPDATE SET team_id = $4, team_role = 0, status = 2`, event.ID, captainID, itNow, team.ID); err != nil {
			t.Fatal(err)
		}
		teamChallenge, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID,
			EventChallengeID: challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		for i, correct := range []bool{false, true} {
			if _, err = db.Queries.CreateChallengeAttempt(ctx, postgres.CreateChallengeAttemptParams{
				ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID, TeamChallengeID: teamChallenge.ID, UserID: captainID,
				Answer: "secret", Correct: correct, ReceivedAt: solvedAt.Add(time.Duration(i-1) * time.Second), CreatedAt: solvedAt,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallenge.ID, SolvedAt: solvedAt}); err != nil {
			t.Fatal(err)
		}
		return seeded{team: team.ID, teamChallenge: teamChallenge.ID, captain: captainID}
	}
	first := seed("first", itNow.Add(time.Minute))
	second := seed("second", itNow.Add(2*time.Minute))
	if _, err := db.Pool.Exec(ctx, `INSERT INTO team_challenge_hint_unlocks (team_challenge_id, hint_id, event_id, event_team_id, event_challenge_id, unlocked_by, unlocked_at, cost)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0)`, second.teamChallenge, uuid.Must(uuid.NewV7()), event.ID, second.team, challenge.ID, second.captain, itNow.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		seeded     seeded
		firstBlood bool
		hints      int64
	}{{first, true, 0}, {second, false, 1}} {
		solves, err := db.Queries.ListParticipationSolves(ctx, postgres.ListParticipationSolvesParams{EventID: event.ID, EventTeamID: tc.seeded.team})
		if err != nil || len(solves) != 1 {
			t.Fatalf("solves of the team only: %+v err=%v", solves, err)
		}
		if solves[0].SolvedBy != tc.seeded.captain || solves[0].FirstBlood != tc.firstBlood || solves[0].Category == "" || solves[0].SolvedByName == "" {
			t.Fatalf("solve = %+v, want solver %s, first blood %v", solves[0], tc.seeded.captain, tc.firstBlood)
		}
		members, err := db.Queries.ListParticipationMembers(ctx, postgres.ListParticipationMembersParams{EventID: event.ID, EventTeamID: tc.seeded.team})
		if err != nil || len(members) != 1 || members[0].UserID != tc.seeded.captain || members[0].Attempts != 2 || members[0].CorrectAttempts != 1 || members[0].Hints != tc.hints {
			t.Fatalf("members = %+v err=%v", members, err)
		}
		totals, err := db.Queries.GetParticipationTeamTotals(ctx, postgres.GetParticipationTeamTotalsParams{EventID: event.ID, EventTeamID: tc.seeded.team})
		if err != nil || totals.Attempts != 2 || totals.CorrectAttempts != 1 || totals.Hints != tc.hints {
			t.Fatalf("totals = %+v err=%v", totals, err)
		}
	}
}
