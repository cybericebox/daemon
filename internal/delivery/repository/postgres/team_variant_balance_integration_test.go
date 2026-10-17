package postgres_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The variant use of an event exercise counts distinct teams over all its
// challenges, only of that event exercise, without the moderators team.
func TestVariantUse_CountsTeamsOfOneEventExerciseWithoutModerators(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "variantuse")
	first := mustCreateScoringChallenge(t, db.Queries, event.ID)
	second, err := db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventExerciseID: first.EventExerciseID, TaskID: uuid.Must(uuid.NewV7()),
		OrderIndex: 1, Points: 100, Snapshot: []byte(`{}`), CreatedAt: itNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE exercises SET name = name || ' (first)'`); err != nil {
		t.Fatal(err)
	}
	other := mustCreateScoringChallenge(t, db.Queries, event.ID)
	team := func(name string) uuid.UUID {
		captain := mustSeedUser(t, db, "variant-"+name+"@test.test")
		value, newErr := eventTeamModel.New(event.ID, captain, name, "variant-code-"+name, itNow)
		if newErr != nil {
			t.Fatal(newErr)
		}
		if value, newErr = eventTeamRepo.New(db.Queries).Create(ctx, value); newErr != nil {
			t.Fatal(newErr)
		}
		return value.ID
	}
	assign := func(teamID, challengeID uuid.UUID, variant int32) {
		if _, assignErr := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: teamID, EventChallengeID: challengeID,
			VariantIndex: variant, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow,
		}); assignErr != nil {
			t.Fatal(assignErr)
		}
	}
	red, blue, green, moderators := team("red"), team("blue"), team("green"), team("mods")
	if _, err = db.Pool.Exec(ctx, `UPDATE event_teams SET moderators = true, hidden = true WHERE id = $1`, moderators); err != nil {
		t.Fatal(err)
	}
	assign(red, first.ID, 0)
	assign(red, second.ID, 0)
	assign(blue, first.ID, 2)
	assign(moderators, first.ID, 1)
	assign(green, other.ID, 1)

	repo := teamChallengeRepo.New(db.Queries)
	used, err := repo.VariantUse(ctx, first.EventExerciseID)
	if err != nil || len(used) != 2 || used[0] != 1 || used[2] != 1 {
		t.Fatalf("variant use = %v, %v; want {0:1 2:1}", used, err)
	}
	if index, found, getErr := repo.TeamVariant(ctx, blue, first.EventExerciseID); getErr != nil || !found || index != 2 {
		t.Fatalf("blue variant = %d %t %v", index, found, getErr)
	}
	if _, found, getErr := repo.TeamVariant(ctx, green, first.EventExerciseID); getErr != nil || found {
		t.Fatalf("green has no assignment of this exercise: %t %v", found, getErr)
	}
}

// The pick lock is held by the first transaction until it ends.
func TestLockVariantPick_SerializesOneEventExercise(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exerciseID := uuid.Must(uuid.NewV7())
	holder, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if err = teamChallengeRepo.New(postgres.New(holder)).LockVariantPick(ctx, exerciseID); err != nil {
		t.Fatal(err)
	}
	try := func(id uuid.UUID) bool {
		var locked bool
		if scanErr := db.Pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('event-exercise-variant:' || $1::uuid, 0))`, id).Scan(&locked); scanErr != nil {
			t.Fatal(scanErr)
		}
		return locked
	}
	if try(exerciseID) {
		t.Fatal("a second transaction took the pick lock of the same event exercise")
	}
	if !try(uuid.Must(uuid.NewV7())) {
		t.Fatal("another event exercise must not wait")
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !try(exerciseID) {
		t.Fatal("the pick lock outlived its transaction")
	}
}
