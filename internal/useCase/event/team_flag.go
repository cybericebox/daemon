package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// teamFlagSlots reads each team's flag slot once per transaction: the count of
// the event's teams created before it (moderators team excluded), read in the
// transaction that writes the team's expected flags.
type teamFlagSlots struct {
	repo    *teamChallengeRepo.Repository
	eventID uuid.UUID
	slots   map[uuid.UUID]uint64
}

func newTeamFlagSlots(repo *teamChallengeRepo.Repository, eventID uuid.UUID) *teamFlagSlots {
	return &teamFlagSlots{repo: repo, eventID: eventID, slots: map[uuid.UUID]uint64{}}
}

func (s *teamFlagSlots) slot(ctx context.Context, teamID uuid.UUID) (uint64, error) {
	if slot, ok := s.slots[teamID]; ok {
		return slot, nil
	}
	slot, err := s.repo.FlagSlot(ctx, s.eventID, teamID)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to read team flag slot").Err()
	}
	s.slots[teamID] = slot
	return slot, nil
}

// resolveTeamFlag is a team's expected flag for one task of a variant: with
// candidates the value of the team's slot under the task's permutation (teams
// 0..N-1 of the event get distinct values), without candidates the full random
// flag. The slot is read only when the task has candidates.
func (u *EventUseCase) resolveTeamFlag(ctx context.Context, slots *teamFlagSlots, teamID, eventExerciseID uuid.UUID, variantIndex int32, task exerciseModel.Task) (string, error) {
	if len(task.Flag) == 0 {
		return teamChallengeModel.ResolveTeamFlag(nil, u.flagRandomBytes, 0, nil)
	}
	slot, err := slots.slot(ctx, teamID)
	if err != nil {
		return "", err
	}
	key := teamChallengeModel.FlagSlotKey(u.flagSlotSecret, eventExerciseID, task.ID, variantIndex)
	return teamChallengeModel.ResolveTeamFlag(task.Flag, u.flagRandomBytes, slot, key)
}
