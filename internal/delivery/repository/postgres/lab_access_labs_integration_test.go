package postgres_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The lab ACL opens a lab only for a published task of an attached set whose prerequisites the team solved,
// like the lab link; the moderators team keeps the readiness rule alone.
func TestListEventLabAccessLabs_HonoursPublicationAndPrerequisites(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "labacl")
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at, deployed_at)
VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, f.group, f.lab, anStart)
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at, deployed_at)
VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`, uuid.Must(uuid.NewV7()), f.event, f.moderators, f.challenge, "mod-"+f.group, f.lab, anStart)

	available := func(team uuid.UUID) bool {
		t.Helper()
		labs, err := db.Queries.ListEventLabAccessLabs(ctx, team)
		if err != nil || len(labs) != 1 {
			t.Fatalf("labs=%v err=%v", labs, err)
		}
		return labs[0].Available
	}

	rtExec(t, db, `UPDATE event_challenges SET published = false WHERE id = $1`, f.challenge)
	if available(f.team) {
		t.Fatal("an unpublished task must stay closed")
	}
	if !available(f.moderators) {
		t.Fatal("the moderators team tests unpublished tasks")
	}
	rtExec(t, db, `UPDATE event_challenges SET published = true WHERE id = $1`, f.challenge)
	if !available(f.team) {
		t.Fatal("a published task without prerequisites is open")
	}

	// A prerequisite the team has not solved closes the lab until the solve exists.
	prerequisite := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_challenges (id, event_exercise_id, task_id, order_index, points, hints_enabled, published, snapshot, created_at)
SELECT $1, event_exercise_id, $2, order_index + 1, points, hints_enabled, true, snapshot, created_at FROM event_challenges WHERE id = $3`, prerequisite, uuid.Must(uuid.NewV7()), f.challenge)
	rtExec(t, db, `INSERT INTO event_challenge_prerequisites (challenge_id, prerequisite_challenge_id) VALUES ($1, $2)`, f.challenge, prerequisite)
	if available(f.team) {
		t.Fatal("an unsolved prerequisite must close the lab")
	}
	tc, err := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.event, EventTeamID: f.team, EventChallengeID: prerequisite,
		Snapshot: []byte(`{}`), ExpectedFlag: "ICE{p}", Readiness: 2, CreatedAt: anStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if available(f.team) {
		t.Fatal("an assigned but unsolved prerequisite must close the lab")
	}
	rtExec(t, db, `INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $2)`, tc.ID, anStart)
	if !available(f.team) {
		t.Fatal("a solved prerequisite opens the lab")
	}
}
