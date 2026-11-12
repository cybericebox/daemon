package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// CleanupWithdrawnLaboratories performs the irreversible part of an event's
// lifecycle. An event whose labs end with its active reservation (labsEndWithReservation) is left to the end of the
// reservation window; every other event, one without a reservation or with a cancelled one included, is cleaned
// here, so no lab, VPN key or access is left without a point of deletion. Finish deliberately retains labs and VPN credentials for review;
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
	bindings = u.withoutReservedEvents(ctx, bindings)
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
		held, e := u.retentionHoldsGroup(ctx, group)
		if e != nil {
			return e
		}
		if held {
			continue
		}
		agent := u.groupAgent(ctx, group)
		if err := u.infra.DestroyLabGroup(ctx, group); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to destroy withdrawn laboratory group").Err()
		}
		// The destroyed group and its labs are settled, so the lifecycle never chases them.
		if err := u.endLifecyclesWithReservation(ctx, group, time.Now().UTC()); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to settle the lifecycle of a withdrawn laboratory group").Err()
		}
		u.releaseTeamUnit(ctx, group, agent)
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

// withoutReservedEvents drops the bindings of the events whose labs are deleted by the end of their reservation
// window (labsEndWithReservation): the withdrawal does not touch them. The decision is made once per event.
func (u *EventUseCase) withoutReservedEvents(ctx context.Context, bindings []labBindingModel.Binding) []labBindingModel.Binding {
	if _, ok := u.resourceGate.(reservationHolder); !ok {
		return bindings // no calendar: every withdrawn event is cleaned up here
	}
	decided := map[uuid.UUID]bool{}
	kept := bindings[:0:0]
	for _, binding := range bindings {
		reserved, seen := decided[binding.EventID]
		if !seen {
			var withdrawAt *time.Time
			if e, err := u.events.GetByID(ctx, binding.EventID); err == nil {
				withdrawAt = e.Lifecycle.WithdrawAt
			}
			reserved = u.labsEndWithReservation(ctx, binding.EventID, withdrawAt)
			decided[binding.EventID] = reserved
		}
		if !reserved {
			kept = append(kept, binding)
		}
	}
	return kept
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
		held, e := u.retentionHoldsGroup(ctx, group)
		if e != nil {
			return e
		}
		if held {
			continue
		}
		agent := u.groupAgent(ctx, group)
		if err := u.infra.DestroyLabGroup(ctx, group); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to destroy queued laboratory group").Err()
		}
		// The rows of a deleted team or event survive it (no foreign key): settle
		// them with the group, so the lifecycle never chases the gone objects.
		if err := u.endLifecyclesWithReservation(ctx, group, time.Now().UTC()); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to settle the lifecycle of a queued laboratory group").Err()
		}
		u.releaseTeamUnit(ctx, group, agent)
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
