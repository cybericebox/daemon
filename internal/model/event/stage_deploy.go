package eventModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// SetLoad is one active exercise set with the pods it puts on the cluster for all teams.
type SetLoad struct {
	ExerciseID uuid.UUID
	// StageID is nil for a set that lives for the whole event.
	StageID *uuid.UUID
	Pods    int
}

// StageDeployPlan says which sets' labs are due for deployment at a moment: the labs of the event's first moment
// (sets without a stage and the first stage) are due from startAt - lead; a later stage's labs are due at
// opens_at - lead, and never before the previous stage opened (a break shorter than the lead starts deploying as
// soon as the previous stage opens). A set of a stage that has opened is always due.
// lead maps the pods to start to the lead before they are needed; initialExtra are the pods of the groups
// themselves (VPN, gateway) that exist only at the event start.
type StageDeployPlan struct {
	InitialLead time.Duration
	// DueAt is the moment each set's labs become due.
	DueAt map[uuid.UUID]time.Time
}

// PlanStageDeploy computes when the labs of every set become due.
func PlanStageDeploy(startAt time.Time, stages []Stage, sets []SetLoad, initialExtra int, lead func(pods int) time.Duration) StageDeployPlan {
	ordered := sortedStages(stages)
	podsByStage := map[uuid.UUID]int{}
	initial := initialExtra
	firstID := uuid.Nil
	if len(ordered) > 0 {
		firstID = ordered[0].ID
	}
	for _, set := range sets {
		if set.StageID == nil || *set.StageID == firstID {
			initial += set.Pods
			continue
		}
		podsByStage[*set.StageID] += set.Pods
	}
	plan := StageDeployPlan{InitialLead: lead(initial), DueAt: make(map[uuid.UUID]time.Time, len(sets))}
	initialDue := startAt.Add(-plan.InitialLead)
	stageDue := make(map[uuid.UUID]time.Time, len(ordered))
	for i, stage := range ordered {
		if i == 0 {
			stageDue[stage.ID] = initialDue
			continue
		}
		due := stage.OpensAt.Add(-lead(podsByStage[stage.ID]))
		if previous := ordered[i-1].OpensAt; due.Before(previous) {
			due = previous
		}
		stageDue[stage.ID] = due
	}
	for _, set := range sets {
		if set.StageID == nil {
			plan.DueAt[set.ExerciseID] = initialDue
			continue
		}
		if due, ok := stageDue[*set.StageID]; ok {
			plan.DueAt[set.ExerciseID] = due
		} else {
			plan.DueAt[set.ExerciseID] = initialDue
		}
	}
	return plan
}

// NotDue lists the sets whose labs are not due yet at now. A set of a stage that has already opened is due at
// once, whatever the arithmetic says (a set added to an open stage deploys immediately).
func (p StageDeployPlan) NotDue(now time.Time, stages []Stage, sets []SetLoad) []uuid.UUID {
	opened := map[uuid.UUID]bool{}
	for _, stage := range stages {
		opened[stage.ID] = stage.Opened(now)
	}
	out := make([]uuid.UUID, 0)
	for _, set := range sets {
		if set.StageID != nil && opened[*set.StageID] {
			continue
		}
		if due, ok := p.DueAt[set.ExerciseID]; ok && now.Before(due) {
			out = append(out, set.ExerciseID)
		}
	}
	return out
}
