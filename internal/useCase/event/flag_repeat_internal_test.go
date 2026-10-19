package event

import (
	"strconv"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/flagFilterRepo"
)

func fixedFlags(n int) []string {
	flags := make([]string, n)
	for i := range flags {
		flags[i] = "ICE{fixed_" + strconv.Itoa(i) + "}"
	}
	return flags
}

// A pool of distinct flags is drawn without repeats until every flag is given:
// the warning compares the pool with the teams that draw from it, never with a
// default capacity, and says why.
func TestFlagsRepeat(t *testing.T) {
	exerciseID, taskID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	oneTask := func(n int) []map[uuid.UUID][]string { return []map[uuid.UUID][]string{{taskID: fixedFlags(n)}} }
	reset := map[flagFilterRepo.Key]int{{EventExerciseID: exerciseID, VariantIndex: 0, TaskID: taskID}: 1}
	for _, tc := range []struct {
		name   string
		flags  int
		teams  flagTeams
		resets map[flagFilterRepo.Key]int
		want   *FlagsRepeatView
	}{
		// Kharkiv Regional CTF: no team limit and only the moderators team.
		{"only the moderators team, no limit", 15, flagTeams{Count: 1}, nil, nil},
		{"15 flags, 3 teams, no limit", 15, flagTeams{Count: 3}, nil, nil},
		{"15 flags, limit 10", 15, flagTeams{Count: 10, Limit: true}, nil, nil},
		{"15 flags, limit 40", 15, flagTeams{Count: 40, Limit: true}, nil, &FlagsRepeatView{Flags: 15, Teams: 40, Reason: FlagsRepeatLimit}},
		{"15 flags, 16 teams, no limit", 15, flagTeams{Count: 16}, nil, &FlagsRepeatView{Flags: 15, Teams: 16, Reason: FlagsRepeatTeams}},
		{"already used up", 15, flagTeams{Count: 3}, reset, &FlagsRepeatView{Flags: 15, Teams: 3, Reason: FlagsRepeatRepeated}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := flagsRepeat(exerciseID, oneTask(tc.flags), tc.teams, tc.resets)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("flagsRepeat = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The smallest pool of the set is the one reported.
func TestFlagsRepeatReportsTheSmallestPool(t *testing.T) {
	exerciseID := uuid.Must(uuid.NewV7())
	variants := []map[uuid.UUID][]string{{uuid.Must(uuid.NewV7()): fixedFlags(8)}, {uuid.Must(uuid.NewV7()): fixedFlags(5)}}
	got := flagsRepeat(exerciseID, variants, flagTeams{Count: 20, Limit: true}, nil)
	if got == nil || got.Flags != 5 {
		t.Fatalf("flagsRepeat = %+v, want the 5-flag pool", got)
	}
}
