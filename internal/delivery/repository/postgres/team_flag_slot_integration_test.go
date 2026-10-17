package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The flag slot of a team that keeps its variant counts the other teams on the
// same variant of the same event exercise whose first row is earlier, strictly,
// without the moderators team; a team without rows counts all of them.
func TestEarlierVariantTeams_CountsEarlierTeamsOnTheVariant(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "flagslot")
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
		captain := mustSeedUser(t, db, "flagslot-"+name+"@test.test")
		value, newErr := eventTeamModel.New(event.ID, captain, name, "flagslot-code-"+name, itNow)
		if newErr != nil {
			t.Fatal(newErr)
		}
		if value, newErr = eventTeamRepo.New(db.Queries).Create(ctx, value); newErr != nil {
			t.Fatal(newErr)
		}
		return value.ID
	}
	assign := func(teamID, challengeID uuid.UUID, variant int32, minute int) {
		if _, assignErr := db.Queries.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: teamID, EventChallengeID: challengeID,
			VariantIndex: variant, Snapshot: []byte(`{}`), ExpectedFlag: "ICE{x}", CreatedAt: itNow.Add(time.Duration(minute) * time.Minute),
		}); assignErr != nil {
			t.Fatal(assignErr)
		}
	}
	early, red, moderators, blue, green, teal, fresh := team("early"), team("red"), team("mods"), team("blue"), team("green"), team("teal"), team("fresh")
	if _, err = db.Pool.Exec(ctx, `UPDATE event_teams SET moderators = true, hidden = true WHERE id = $1`, moderators); err != nil {
		t.Fatal(err)
	}
	assign(early, other.ID, 0, 0) // another event exercise: never counted
	assign(red, first.ID, 0, 1)
	assign(red, second.ID, 0, 9) // a later row of red does not move its first one
	assign(moderators, first.ID, 0, 2)
	assign(blue, first.ID, 1, 3)
	assign(green, first.ID, 0, 4)
	assign(teal, second.ID, 0, 5)

	repo := teamChallengeRepo.New(db.Queries)
	for _, tc := range []struct {
		name    string
		id      uuid.UUID
		variant int32
		want    int
	}{
		{"red", red, 0, 0}, {"moderators", moderators, 0, 1}, {"green", green, 0, 1}, {"teal", teal, 0, 2},
		{"blue", blue, 1, 0}, {"fresh team without rows", fresh, 0, 3}, {"fresh on variant 1", fresh, 1, 1},
	} {
		got, getErr := repo.EarlierVariantTeams(ctx, first.EventExerciseID, tc.id, tc.variant)
		if getErr != nil || got != tc.want {
			t.Fatalf("%s slot = %d, %v; want %d", tc.name, got, getErr, tc.want)
		}
	}

	// The pass snapshot gives the teams that stay on a variant the same slots.
	entries, err := repo.TeamVariantSnapshot(ctx, first.EventExerciseID)
	if err != nil || len(entries) != 5 {
		t.Fatalf("snapshot = %+v, %v; want 5 (team, variant) entries", entries, err)
	}
	snapshot := teamChallengeModel.NewFlagSlotSnapshot(entries)
	for _, tc := range []struct {
		name    string
		id      uuid.UUID
		variant int32
		want    uint64
	}{{"red", red, 0, 0}, {"moderators", moderators, 0, 1}, {"green", green, 0, 1}, {"teal", teal, 0, 2}, {"blue", blue, 1, 0}} {
		if got := snapshot.Slot(tc.id, tc.variant); got != tc.want {
			t.Fatalf("snapshot %s slot = %d, want %d", tc.name, got, tc.want)
		}
	}
}
