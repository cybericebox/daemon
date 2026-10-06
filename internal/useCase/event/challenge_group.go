package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeGroupRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventChallengeGroupModel "github.com/cybericebox/daemon/internal/model/eventChallengeGroup"
)

func (u *EventUseCase) CreateChallengeGroup(ctx context.Context, eventID uuid.UUID, in CreateChallengeGroupInput) (ChallengeGroupView, error) {
	group, err := eventChallengeGroupModel.New(eventID, in.Name, in.Order, time.Now())
	if err != nil {
		return ChallengeGroupView{}, err
	}
	created, err := u.challengeGroups.Create(ctx, group)
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, eventChallengeGroupModel.ErrChallengeGroupExists); ok {
			return ChallengeGroupView{}, creator.Err()
		}
		return ChallengeGroupView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create challenge group").Err()
	}
	return toChallengeGroupView(created), nil
}

func (u *EventUseCase) ListChallengeGroups(ctx context.Context, eventID uuid.UUID) ([]ChallengeGroupView, error) {
	groups, err := u.challengeGroups.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge groups").Err()
	}
	items := make([]ChallengeGroupView, 0, len(groups))
	for _, group := range groups {
		items = append(items, toChallengeGroupView(group))
	}
	return items, nil
}

func (u *EventUseCase) UpdateChallengeGroup(ctx context.Context, eventID, groupID uuid.UUID, in UpdateChallengeGroupInput) (ChallengeGroupView, error) {
	groups, err := u.challengeGroups.List(ctx, eventID)
	if err != nil {
		return ChallengeGroupView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge groups").Err()
	}
	for _, group := range groups {
		if group.ID != groupID {
			continue
		}
		if err = group.Update(in.Name, in.Order); err != nil {
			return ChallengeGroupView{}, err
		}
		updated, updateErr := u.challengeGroups.Update(ctx, group)
		if updateErr != nil {
			if repositoryTools.IsObjectNotFoundError(updateErr) {
				return ChallengeGroupView{}, eventChallengeGroupModel.ErrChallengeGroupNotFound.Err()
			}
			if creator, ok := repositoryTools.UniqueViolationError(updateErr, eventChallengeGroupModel.ErrChallengeGroupUpdateConflict); ok {
				return ChallengeGroupView{}, creator.Err()
			}
			return ChallengeGroupView{}, model.ErrPlatform.WithError(updateErr).WithMessage("Failed to update challenge group").Err()
		}
		return toChallengeGroupView(updated), nil
	}
	return ChallengeGroupView{}, eventChallengeGroupModel.ErrChallengeGroupNotFound.Err()
}

// ReorderChallengeGroups assigns a complete manager-provided group order.
// Orders are first moved to a disjoint negative range, then written back
// sequentially in one transaction, so swaps cannot trip the UNIQUE
// (event_id, order_index) constraint midway through the operation.
func (u *EventUseCase) ReorderChallengeGroups(ctx context.Context, eventID uuid.UUID, in ReorderChallengeGroupsInput) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	groups := eventChallengeGroupRepo.New(txRepo)
	current, err := groups.List(txCtx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge groups").Err()
	}
	if !sameGroupSet(current, in.GroupIDs) {
		return eventChallengeGroupModel.ErrChallengeGroupOrderInvalid.Err()
	}
	if err = groups.VacateOrders(txCtx, eventID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to prepare challenge group order").Err()
	}
	for order, id := range in.GroupIDs {
		affected, setErr := groups.SetOrder(txCtx, eventID, id, int32(order))
		if setErr != nil {
			return model.ErrPlatform.WithError(setErr).WithMessage("Failed to reorder challenge group").Err()
		}
		if affected != 1 {
			return eventChallengeGroupModel.ErrChallengeGroupOrderInvalid.Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reorder challenge groups").Err()
	}
	return nil
}

func sameGroupSet(current []eventChallengeGroupModel.Group, proposed []uuid.UUID) bool {
	if len(current) != len(proposed) {
		return false
	}
	known := make(map[uuid.UUID]struct{}, len(current))
	for _, group := range current {
		known[group.ID] = struct{}{}
	}
	for _, id := range proposed {
		if _, ok := known[id]; !ok {
			return false
		}
		delete(known, id)
	}
	return len(known) == 0
}

func (u *EventUseCase) DeleteChallengeGroup(ctx context.Context, eventID, groupID uuid.UUID) error {
	affected, err := u.challengeGroups.Delete(ctx, eventID, groupID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete challenge group").Err()
	}
	if affected == 0 {
		return eventChallengeGroupModel.ErrChallengeGroupNotFound.Err()
	}
	return nil
}

func toChallengeGroupView(value eventChallengeGroupModel.Group) ChallengeGroupView {
	return ChallengeGroupView{ID: value.ID, Name: value.Name, Order: value.Order, CreatedAt: value.CreatedAt}
}
