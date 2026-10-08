package event

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/model"
)

// CleanupWithdrawnLaboratories performs the irreversible part of an event's
// lifecycle. Finish deliberately retains labs and VPN credentials for review;
// withdrawal removes each event-team LabGroup (and therefore every contained
// challenge lab and VPN client) before recording the durable terminal state.
//
// LabGroup names are shared by a team's bindings across exercises, so the
// delete is intentionally de-duplicated. If the agent call or persistence
// fails, the binding stays eligible for the next periodic pass.
func (u *EventUseCase) CleanupWithdrawnLaboratories(ctx context.Context) error {
	if err := u.labBindings.QueueWithdrawnEmptyGroups(ctx, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to queue withdrawn VPN-only groups").Err()
	}
	bindings, err := u.labBindings.ListWithdrawn(ctx, time.Now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list withdrawn laboratory bindings").Err()
	}
	if len(bindings) == 0 {
		return nil
	}
	if u.infra == nil {
		return infraUnavailable()
	}
	if u.infrastructureCapability != nil {
		if err = u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
			return err
		}
	}

	byGroup := make(map[string][]int, len(bindings))
	for index, binding := range bindings {
		byGroup[binding.LabGroupName] = append(byGroup[binding.LabGroupName], index)
	}
	for group, indexes := range byGroup {
		if u.lifecycleControls {
			owned, e := eventLabRetentionRepo.New(u.repo).OwnedGroup(ctx, group)
			if e != nil {
				return e
			}
			if owned {
				continue
			}
		}
		if err := u.infra.DestroyLabGroup(ctx, group); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to destroy withdrawn laboratory group").Err()
		}
		for _, index := range indexes {
			affected, markErr := u.labBindings.MarkDestroyed(ctx, bindings[index].ID)
			if markErr != nil {
				return model.ErrPlatform.WithError(markErr).WithMessage("Failed to mark destroyed laboratory binding").Err()
			}
			if affected != 1 {
				return model.ErrPlatform.WithMessage("Laboratory binding changed concurrently during cleanup").Err()
			}
		}
	}
	return nil
}

// CleanupQueuedLabGroups handles teams deleted before their LabGroup can be
// destroyed. The request survives the cascading removal of lab_bindings, so a
// temporary agent outage cannot strand an unreachable group indefinitely.
func (u *EventUseCase) CleanupQueuedLabGroups(ctx context.Context) error {
	groups, err := u.labBindings.ListPendingCleanupRequests(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list queued laboratory group cleanup requests").Err()
	}
	if len(groups) == 0 {
		return nil
	}
	if u.infra == nil {
		return infraUnavailable()
	}
	if u.infrastructureCapability != nil {
		if err = u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
			return err
		}
	}
	for _, group := range groups {
		if u.lifecycleControls {
			owned, e := eventLabRetentionRepo.New(u.repo).OwnedGroup(ctx, group)
			if e != nil {
				return e
			}
			if owned {
				continue
			}
		}
		if err := u.infra.DestroyLabGroup(ctx, group); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to destroy queued laboratory group").Err()
		}
		affected, markErr := u.labBindings.MarkCleanupRequestDestroyed(ctx, group, time.Now())
		if markErr != nil {
			return model.ErrPlatform.WithError(markErr).WithMessage("Failed to mark queued laboratory group cleanup complete").Err()
		}
		if affected != 1 {
			return model.ErrPlatform.WithMessage("Laboratory group cleanup request changed concurrently").Err()
		}
	}
	return nil
}
