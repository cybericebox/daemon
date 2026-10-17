package postgres_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// A team's flag slot counts the teams of its event with a smaller (UUIDv7) id,
// strictly, the moderators team and other events' teams excluded.
func TestFlagSlot_CountsEarlierTeamsOfTheEventWithoutModerators(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "flagslot")
	other := mustSeedEventForParticipants(t, db, "flagslotother")
	team := func(eventID uuid.UUID, name string) uuid.UUID {
		captain := mustSeedUser(t, db, "flagslot-"+name+"@test.test")
		value, err := eventTeamModel.New(eventID, captain, name, "flagslot-code-"+name, itNow)
		if err != nil {
			t.Fatal(err)
		}
		if value, err = eventTeamRepo.New(db.Queries).Create(ctx, value); err != nil {
			t.Fatal(err)
		}
		return value.ID
	}
	// UUIDv7 ids are created in this order.
	red := team(event.ID, "red")
	moderators := team(event.ID, "mods")
	foreign := team(other.ID, "foreign")
	blue := team(event.ID, "blue")
	green := team(event.ID, "green")
	if _, err := db.Pool.Exec(ctx, `UPDATE event_teams SET moderators = true, hidden = true WHERE id = $1`, moderators); err != nil {
		t.Fatal(err)
	}
	repo := teamChallengeRepo.New(db.Queries)
	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want uint64
	}{{"red", red, 0}, {"moderators", moderators, 1}, {"blue", blue, 1}, {"green", green, 2}} {
		got, err := repo.FlagSlot(ctx, event.ID, tc.id)
		if err != nil || got != tc.want {
			t.Fatalf("%s slot = %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
	if got, err := repo.FlagSlot(ctx, other.ID, foreign); err != nil || got != 0 {
		t.Fatalf("foreign slot = %d, %v; want 0", got, err)
	}
}
