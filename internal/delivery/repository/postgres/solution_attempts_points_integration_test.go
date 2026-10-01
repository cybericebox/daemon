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

// Only the attempt that solved the task carries the task's points in the
// attempts journal; earlier wrong and later repeated answers carry none.
func TestListEventSolutionAttemptsPointsOnSolvingAttempt(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "attemptpoints")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	captainID := mustSeedUser(t, db, "attemptpoints@test.test")
	team, err := eventTeamModel.New(event.ID, captainID, "Team", "join-code-points", itNow)
	if err != nil {
		t.Fatalf("new team: %v", err)
	}
	if team, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	teamChallenge, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team.ID,
		EventChallengeID: challenge.ID, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
	})
	if err != nil {
		t.Fatalf("create team challenge: %v", err)
	}
	attempts := map[string]time.Time{"wrong": itNow, "solve": itNow.Add(time.Minute), "repeat": itNow.Add(2 * time.Minute)}
	ids := map[uuid.UUID]string{}
	for name, at := range attempts {
		id := uuid.Must(uuid.NewV7())
		ids[id] = name
		if _, err = db.Queries.CreateChallengeAttempt(ctx, postgres.CreateChallengeAttemptParams{
			ID: id, EventID: event.ID, EventTeamID: team.ID, TeamChallengeID: teamChallenge.ID, UserID: captainID,
			Answer: name, Correct: name != "wrong", ReceivedAt: at, CreatedAt: at,
		}); err != nil {
			t.Fatalf("create attempt %s: %v", name, err)
		}
	}
	if err = db.Queries.UpsertTeamChallengeSolve(ctx, postgres.UpsertTeamChallengeSolveParams{
		TeamChallengeID: teamChallenge.ID, SolvedAt: attempts["solve"], AwardedPoints: pgtype.Int4{Int32: 100, Valid: true},
	}); err != nil {
		t.Fatalf("store solve: %v", err)
	}

	rows, err := db.Queries.ListEventSolutionAttempts(ctx, postgres.ListEventSolutionAttemptsParams{
		EventID: event.ID, CursorReceivedAt: itNow.Add(time.Hour), CursorID: uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff")), LimitVal: 10,
	})
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for _, row := range rows {
		name := ids[row.ID]
		if want := name == "solve"; row.Scored != want {
			t.Fatalf("%s: scored = %v, want %v", name, row.Scored, want)
		}
		if name == "solve" && row.Points != 100 {
			t.Fatalf("solve points = %d, want 100", row.Points)
		}
	}
}
