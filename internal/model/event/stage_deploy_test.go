package eventModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

func lead(pods int) time.Duration { return time.Duration(10+pods) * time.Minute }

func TestPlanStageDeployDueTimes(t *testing.T) {
	a, b := stage("A", 0, 4, false), stage("B", 6, 10, false)
	unstaged, inA, inB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	sets := []eventModel.SetLoad{
		{ExerciseID: unstaged, Pods: 5},
		{ExerciseID: inA, StageID: &a.ID, Pods: 5},
		{ExerciseID: inB, StageID: &b.ID, Pods: 20},
	}
	plan := eventModel.PlanStageDeploy(at(0), []eventModel.Stage{a, b}, sets, 2, lead)
	// the first moment: unstaged + the first stage + the group pods
	if want := lead(12); plan.InitialLead != want {
		t.Fatalf("initial lead = %v, want %v", plan.InitialLead, want)
	}
	initialDue := at(0).Add(-lead(12))
	if !plan.DueAt[unstaged].Equal(initialDue) || !plan.DueAt[inA].Equal(initialDue) {
		t.Fatalf("unstaged and first-stage labs are due at the event deploy time: %v %v", plan.DueAt[unstaged], plan.DueAt[inA])
	}
	// a later stage: opens_at minus its own lead
	if want := at(6).Add(-lead(20)); !plan.DueAt[inB].Equal(want) {
		t.Fatalf("later stage due = %v, want %v", plan.DueAt[inB], want)
	}
	// before the first deploy time everything waits
	if got := plan.NotDue(initialDue.Add(-time.Minute), []eventModel.Stage{a, b}, sets); len(got) != 3 {
		t.Fatalf("before the initial deploy time all wait: %v", got)
	}
	// during stage A the later stage still waits, until its lead
	notDue := plan.NotDue(at(1), []eventModel.Stage{a, b}, sets)
	if len(notDue) != 1 || notDue[0] != inB {
		t.Fatalf("only the later stage waits: %v", notDue)
	}
	if got := plan.NotDue(at(6).Add(-lead(20)), []eventModel.Stage{a, b}, sets); len(got) != 0 {
		t.Fatalf("labs of a later stage are due at opens_at - lead: %v", got)
	}
}

func TestPlanStageDeployShortBreakStartsWhenThePreviousStageOpens(t *testing.T) {
	a, b := stage("A", 0, 4, false), stage("B", 4, 10, false) // no break at all
	inB := uuid.Must(uuid.NewV7())
	sets := []eventModel.SetLoad{{ExerciseID: inB, StageID: &b.ID, Pods: 500}}
	plan := eventModel.PlanStageDeploy(at(0), []eventModel.Stage{a, b}, sets, 0, func(int) time.Duration { return 6 * time.Hour })
	if !plan.DueAt[inB].Equal(a.OpensAt) {
		t.Fatalf("the lead is longer than the whole stage: deploy starts when the previous stage opens, got %v", plan.DueAt[inB])
	}
}

func TestSetAddedToAnOpenStageIsDueAtOnce(t *testing.T) {
	a, b := stage("A", 0, 4, false), stage("B", 6, 10, false)
	added := uuid.Must(uuid.NewV7())
	sets := []eventModel.SetLoad{{ExerciseID: added, StageID: &a.ID, Pods: 500}}
	plan := eventModel.PlanStageDeploy(at(0), []eventModel.Stage{a, b}, sets, 0, func(int) time.Duration { return 6 * time.Hour })
	// even if the arithmetic put it in the future, an opened stage's labs are never held back
	plan.DueAt[added] = at(100)
	if got := plan.NotDue(at(1), []eventModel.Stage{a, b}, sets); len(got) != 0 {
		t.Fatalf("a set of an open stage must deploy at once: %v", got)
	}
}

func TestPlanStageDeployWithoutStages(t *testing.T) {
	only := uuid.Must(uuid.NewV7())
	sets := []eventModel.SetLoad{{ExerciseID: only, Pods: 10}}
	plan := eventModel.PlanStageDeploy(at(5), nil, sets, 0, lead)
	if !plan.DueAt[only].Equal(at(5).Add(-lead(10))) {
		t.Fatalf("unstaged event due = %v", plan.DueAt[only])
	}
	if got := plan.NotDue(at(5), nil, sets); len(got) != 0 {
		t.Fatalf("after the start nothing waits: %v", got)
	}
}
