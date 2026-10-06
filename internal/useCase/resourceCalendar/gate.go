package resourceCalendarUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// HoldsForAllTeams is the gate of a running event: a new task deploys only if the reservation can hold it for
// ALL teams. perTeam is the plan of one team with the new task, teams the teams there are and device the largest
// device of the tasks. An event without a reservation is not checked (it runs on the platform's free room).
// Otherwise it fails with ErrNotEnoughReserved ("request an extension"); no partial rollout.
func (u *ResourceCalendarUseCase) HoldsForAllTeams(ctx context.Context, eventID uuid.UUID, perTeam Amount, teams int, device Amount) error {
	r, err := u.store.GetEventReservation(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return platformErr(err, "Failed to read the event reservation")
	}
	if teams > r.Teams || r.Unplaced > 0 || !perTeam.Within(r.TeamSlot()) {
		return calModel.ErrNotEnoughReserved.Err()
	}
	// The agents that hold the teams must allow the device (an elevated task only runs where the maxima fit).
	states, err := u.agentStates(ctx, u.now().UTC())
	if err != nil {
		return err
	}
	byID := map[uuid.UUID]calModel.Agent{}
	for _, a := range usedAgents(states) {
		byID[a.ID] = a
	}
	for _, sh := range r.Placement {
		a, ok := byID[sh.AgentID]
		if !ok || !a.Allows(device) {
			return calModel.ErrNotEnoughReserved.Err()
		}
	}
	return nil
}

// HasEventReservation tells whether the event has an active reservation. Labs of an event that needs them wait for
// it: nothing of the event is deployed on the platform's free room.
func (u *ResourceCalendarUseCase) HasEventReservation(ctx context.Context, eventID uuid.UUID) (bool, error) {
	if _, err := u.store.GetEventReservation(ctx, eventID); err != nil {
		if notFound(err) {
			return false, nil
		}
		return false, platformErr(err, "Failed to read the event reservation")
	}
	return true, nil
}
