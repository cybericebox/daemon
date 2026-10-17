package teamChallengeModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

func teamID(n byte) uuid.UUID { return uuid.UUID{15: n} }

var snapshotBase = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

func entry(team byte, variant int32, minute int, moderators bool) teamChallengeModel.TeamVariantEntry {
	return teamChallengeModel.TeamVariantEntry{TeamID: teamID(team), Variant: variant, Moderators: moderators,
		FirstAt: snapshotBase.Add(time.Duration(minute) * time.Minute), FirstID: uuid.UUID{0: team}}
}

// Teams that stay on a variant keep their original slot (earlier teams on it);
// teams moving onto it in the pass take base count + their order in the pass,
// so every team on a variant has a distinct slot. The moderators team takes no slot.
func TestFlagSlotSnapshot_KeptAndMovedTeamsAreDistinct(t *testing.T) {
	s := teamChallengeModel.NewFlagSlotSnapshot([]teamChallengeModel.TeamVariantEntry{
		entry(1, 0, 1, false), entry(2, 0, 2, true), entry(3, 0, 3, false), // on 0: team 1, moderators, team 3
		entry(4, 5, 4, false), entry(5, 6, 5, false), entry(6, 7, 6, false), entry(7, 8, 7, true), // on dropped variants
	})
	cases := []struct {
		team    byte
		variant int32
		want    uint64
	}{
		{3, 0, 1}, {1, 0, 0}, {2, 0, 1}, // kept: original slots; moderators count the team before them
		{4, 0, 2}, {7, 0, 3}, {5, 0, 3}, {6, 0, 4}, // moved onto 0: base 2, then 2, 3, 4 in pass order; moderators take 3 without using it
		{4, 0, 2}, // asked again: the same slot, nothing consumed
	}
	for _, tc := range cases {
		if got := s.Slot(teamID(tc.team), tc.variant); got != tc.want {
			t.Fatalf("team %d on %d = %d, want %d", tc.team, tc.variant, got, tc.want)
		}
	}
	if got := s.Slot(teamID(9), 1); got != 0 {
		t.Fatalf("an unknown team on an empty variant = %d, want 0", got)
	}
}
