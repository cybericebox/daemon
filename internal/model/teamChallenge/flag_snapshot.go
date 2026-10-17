package teamChallengeModel

import (
	"bytes"
	"time"

	"github.com/gofrs/uuid"
)

// TeamVariantEntry is one (team, variant) of an event exercise in a snapshot:
// the team has a row on the variant; FirstAt/FirstID order the team's first row
// of the event exercise.
type TeamVariantEntry struct {
	TeamID     uuid.UUID
	Variant    int32
	Moderators bool
	FirstAt    time.Time
	FirstID    uuid.UUID
}

type snapshotTeam struct {
	moderators bool
	firstAt    time.Time
	firstID    uuid.UUID
}

func (a snapshotTeam) before(b snapshotTeam) bool {
	if !a.firstAt.Equal(b.firstAt) {
		return a.firstAt.Before(b.firstAt)
	}
	return bytes.Compare(a.firstID.Bytes(), b.firstID.Bytes()) < 0
}

// FlagSlotSnapshot assigns the flag slots of one source-switch pass in memory
// from one read taken under the variant pick lock before any row changes, so
// the pass's own updates never shift a count:
//   - a team already on the variant keeps its original slot: the other teams on
//     it (moderators excluded) whose first row is earlier;
//   - a team moving onto the variant takes the variant's team count (moderators
//     excluded) plus the number of teams moved onto it earlier in the pass; the
//     moderators team takes that value without using it up.
//
// A team keeps the first slot it is given on a variant in the pass; on another
// variant it has that variant's own slot.
type FlagSlotSnapshot struct {
	teams   map[uuid.UUID]snapshotTeam
	on      map[int32][]uuid.UUID
	onSet   map[int32]map[uuid.UUID]bool
	movedIn map[int32]int
	given   map[teamVariant]uint64
}

type teamVariant struct {
	team    uuid.UUID
	variant int32
}

func NewFlagSlotSnapshot(entries []TeamVariantEntry) *FlagSlotSnapshot {
	s := &FlagSlotSnapshot{teams: map[uuid.UUID]snapshotTeam{}, on: map[int32][]uuid.UUID{}, onSet: map[int32]map[uuid.UUID]bool{},
		movedIn: map[int32]int{}, given: map[teamVariant]uint64{}}
	for _, e := range entries {
		s.teams[e.TeamID] = snapshotTeam{moderators: e.Moderators, firstAt: e.FirstAt, firstID: e.FirstID}
		if s.onSet[e.Variant] == nil {
			s.onSet[e.Variant] = map[uuid.UUID]bool{}
		}
		if !s.onSet[e.Variant][e.TeamID] {
			s.onSet[e.Variant][e.TeamID] = true
			s.on[e.Variant] = append(s.on[e.Variant], e.TeamID)
		}
	}
	return s
}

// Slot is the team's flag slot on a variant in this pass.
func (s *FlagSlotSnapshot) Slot(teamID uuid.UUID, variant int32) uint64 {
	key := teamVariant{team: teamID, variant: variant}
	if slot, ok := s.given[key]; ok {
		return slot
	}
	me := s.teams[teamID]
	var slot uint64
	if s.onSet[variant][teamID] {
		for _, other := range s.on[variant] {
			if other != teamID && !s.teams[other].moderators && s.teams[other].before(me) {
				slot++
			}
		}
	} else {
		for _, other := range s.on[variant] {
			if !s.teams[other].moderators {
				slot++
			}
		}
		slot += uint64(s.movedIn[variant])
		if !me.moderators {
			s.movedIn[variant]++
		}
	}
	s.given[key] = slot
	return slot
}
