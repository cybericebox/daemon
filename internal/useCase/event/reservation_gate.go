package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// ResourceGate is the resource calendar's check of a running event: whether the event's reservation can hold a
// task for ALL teams. It fails with "not enough reserved resources, request an extension". An event without a
// reservation is not checked.
type ResourceGate interface {
	HoldsForAllTeams(ctx context.Context, eventID uuid.UUID, perTeam resourcesModel.Amount, teams int, device resourcesModel.Amount) error
}

// SetResourceGate wires the calendar after the use cases exist.
func (u *EventUseCase) SetResourceGate(g ResourceGate) { u.resourceGate = g }

// ReservationNeed is what the resource calendar reserves for the event: the plan of one team, the largest device
// any task runs and the teams reserved for.
type ReservationNeed struct {
	Teams         int
	PerTeam       resourcesModel.Amount
	LargestDevice resourcesModel.Amount
}

// ReservationNeed reuses the event's resource plan: the devices of its tasks (the largest variant of each) plus
// the group's own pods, per team, and the teams.
func (u *EventUseCase) ReservationNeed(ctx context.Context, eventID uuid.UUID) (ReservationNeed, error) {
	plan, err := u.GetResourcePlan(ctx, eventID)
	if err != nil {
		return ReservationNeed{}, err
	}
	need := ReservationNeed{Teams: plan.Teams, PerTeam: plan.PerTeam.Amount}
	for _, t := range plan.Tasks {
		need.LargestDevice = need.LargestDevice.Max(t.deviceMax)
	}
	return need, nil
}

// requireReservedForTask refuses a task that a running event's reservation cannot hold for every team: a new
// task deploys for all teams or not at all. Events that are not running, have no lab topology for the task or no
// reservation are not checked.
func (u *EventUseCase) requireReservedForTask(ctx context.Context, eventID uuid.UUID, version exerciseModel.ExerciseVersion, link eventExerciseModel.EventExercise, now time.Time) error {
	if u.resourceGate == nil || !exerciseModel.HasInfrastructure(version.Variants) {
		return nil
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event for the resource check").Err()
	}
	if !e.InfrastructureAllowed || !e.Lifecycle.RuntimeOpen(now) {
		return nil
	}
	plan, err := u.GetResourcePlan(ctx, eventID)
	if err != nil {
		return err
	}
	task := planTask(u.resourcePolicy(), link, version.Variants, u.approvals(ctx, []uuid.UUID{version.ExerciseID})[version.ExerciseID])
	device := task.deviceMax
	for _, t := range plan.Tasks {
		device = device.Max(t.deviceMax)
	}
	return u.resourceGate.HoldsForAllTeams(ctx, eventID, plan.PerTeam.Amount.Add(task.Reserved.Amount), plan.Teams, device)
}
