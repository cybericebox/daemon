package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// teamFlagSlots gives each team of one event exercise its flag slot within one
// transaction: the number of teams already on the same variant (the moderators
// team excluded), read under the per-event-exercise variant pick lock.
//   - A new pick (and a re-pick of a source switch) takes the balance count of
//     the picked variant before the team is counted, so teams on a variant get
//     0, 1, 2, ... in pick order (a pass counts its own earlier picks).
//   - A team that keeps its variant counts the other teams on it that got the
//     event exercise before it, which is the slot it had when it was picked.
type teamFlagSlots struct {
	repo            *teamChallengeRepo.Repository
	eventExerciseID uuid.UUID
	locked          bool
	known           map[uuid.UUID]uint64
}

func newTeamFlagSlots(repo *teamChallengeRepo.Repository, eventExerciseID uuid.UUID) *teamFlagSlots {
	return &teamFlagSlots{repo: repo, eventExerciseID: eventExerciseID, known: map[uuid.UUID]uint64{}}
}

// picked records the slot of a team picked from a balance read under the lock.
func (s *teamFlagSlots) picked(teamID uuid.UUID, balance *teamChallengeModel.VariantBalance, index int32) {
	if s == nil {
		return
	}
	s.locked = true
	s.known[teamID] = uint64(max(balance.Count(index), 0))
}

func (s *teamFlagSlots) slot(ctx context.Context, teamID uuid.UUID, variantIndex int32) (uint64, error) {
	if slot, ok := s.known[teamID]; ok {
		return slot, nil
	}
	if !s.locked {
		if err := s.repo.LockVariantPick(ctx, s.eventExerciseID); err != nil {
			return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to lock the variant pick").Err()
		}
		s.locked = true
	}
	count, err := s.repo.EarlierVariantTeams(ctx, s.eventExerciseID, teamID, variantIndex)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to read team flag slot").Err()
	}
	slot := uint64(max(count, 0))
	s.known[teamID] = slot
	return slot, nil
}

// resolveTeamFlag is a team's expected flag for one task of a variant: with
// candidates the value of the team's slot under the task's permutation (the
// first N teams on a variant get distinct values), without candidates the full
// random flag. The slot is read only when the task has candidates.
func (u *EventUseCase) resolveTeamFlag(ctx context.Context, slots *teamFlagSlots, teamID uuid.UUID, variantIndex int32, task exerciseModel.Task) (string, error) {
	if len(task.Flag) == 0 {
		return teamChallengeModel.ResolveTeamFlag(nil, u.flagRandomBytes, 0, nil)
	}
	slot, err := slots.slot(ctx, teamID, variantIndex)
	if err != nil {
		return "", err
	}
	key := teamChallengeModel.FlagSlotKey(u.flagSlotSecret, slots.eventExerciseID, task.ID, variantIndex)
	return teamChallengeModel.ResolveTeamFlag(task.Flag, u.flagRandomBytes, slot, key)
}
