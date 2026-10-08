package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
)

func plannedUsers(configured, actual int, known, frozen bool) int {
	if frozen && known {
		return max(actual, 1)
	}
	if configured > 0 {
		return configured
	}
	return int(eventConfigModel.DefaultMaxTeamSize())
}

func (u *EventUseCase) validateExistingGroupSizing(ctx context.Context, eventID uuid.UUID, cfg eventConfigModel.EventConfig) error {
	groups, err := eventLabAllocationRepo.New(u.repo).Groups(ctx, eventID)
	if err != nil {
		return err
	}
	planner, ok := u.infra.(resourcePlanner)
	if !ok && len(groups) > 0 {
		return calModel.ErrNotEnoughReserved.Err()
	}
	for _, group := range groups {
		plan := group.Plan
		if cfg.IsTeamMode() && cfg.MaxTeamSize > 0 {
			plan.MaxUsers = int(cfg.MaxTeamSize)
		}
		if limit := cfg.EffectiveLabPolicy().MaxActiveLabsPerTeam; limit != nil {
			plan.MaxActiveLabs = min(plan.MaxActiveLabs, int(*limit))
			plan.InternetLabs = min(plan.InternetLabs, plan.MaxActiveLabs)
		}
		plan.AllowedRelations = plan.MaxUsers * plan.MaxActiveLabs
		if planner.NeedFit(infraModel.PlacementNeed{Plan: plan}) != nil {
			return calModel.ErrNotEnoughReserved.Err()
		}
		sizes, known := planner.GroupSizes(plan)
		if !known || !group.Sizes.Holds(sizes) {
			return calModel.ErrNotEnoughReserved.Err()
		}
	}
	return nil
}
