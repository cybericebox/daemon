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

// requireReservedForChange is the same gate for a task that is already attached and changes what it runs: the pinned
// version moves (update, replace, revert) or the task becomes the event's own copy (fork). The task is deployed
// for every team as it is now, so the plan of one team changes by the difference between the new size of the task
// and its size now, and the largest device may grow. A change that grows neither (the usual case of a fix, or a
// fork, which copies the pinned topology) is not checked: the reservation held the task before. A change that grows
// the task passes only if the reservation can hold the new size for all teams; otherwise it fails with the
// "not enough reserved resources, request an extension" error and nothing is switched.
func (u *EventUseCase) requireReservedForChange(ctx context.Context, eventID uuid.UUID, link eventExerciseModel.EventExercise, version exerciseModel.ExerciseVersion, now time.Time) error {
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
	changed := planTask(u.resourcePolicy(), link, version.Variants, u.approvals(ctx, []uuid.UUID{version.ExerciseID})[version.ExerciseID])
	perTeam, device := plan.PerTeam.Amount, changed.deviceMax
	var before resourcesModel.Amount
	for _, t := range plan.Tasks {
		if t.EventExerciseID == link.ID {
			before = t.Reserved.Amount
			continue
		}
		device = device.Max(t.deviceMax)
	}
	// What the task asks for now comes off, what it will ask for goes on.
	perTeam = resourcesModel.Amount{
		CPUMillicores: perTeam.CPUMillicores - before.CPUMillicores + changed.Reserved.CPUMillicores,
		MemoryBytes:   perTeam.MemoryBytes - before.MemoryBytes + changed.Reserved.MemoryBytes,
	}
	if changed.Reserved.Amount.Within(before) && changed.deviceMax.Within(taskDevice(plan, link.ID)) {
		return nil
	}
	return u.resourceGate.HoldsForAllTeams(ctx, eventID, perTeam, plan.Teams, device)
}

// taskDevice is the largest device of an attached task in the plan; zero when it is not in it.
func taskDevice(plan EventResourcePlan, eventExerciseID uuid.UUID) resourcesModel.Amount {
	for _, t := range plan.Tasks {
		if t.EventExerciseID == eventExerciseID {
			return t.deviceMax
		}
	}
	return resourcesModel.Amount{}
}
