package labCheckModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
)

var t0 = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func at(s int) *time.Time { v := t0.Add(time.Duration(s) * time.Second); return &v }

func stateTask(key string, mode exerciseModel.StateMode, devices ...uuid.UUID) exerciseModel.Task {
	var conditions []exerciseModel.Condition
	for _, d := range devices {
		conditions = append(conditions, exerciseModel.Condition{DeviceID: d, File: &exerciseModel.FileObservation{Path: "/a"}})
	}
	return exerciseModel.Task{Key: key, Check: exerciseModel.Check{State: &exerciseModel.StateCheck{Mode: mode, Conditions: conditions}}}
}

func result(id string, state labCheckModel.State, first *time.Time, changed int) labCheckModel.Result {
	return labCheckModel.Result{CheckID: id, State: state, FirstPassedAt: first, ChangedAt: t0.Add(time.Duration(changed) * time.Second)}
}

func TestExpectedCheckIDsFollowTheDevicesOfTheConditions(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	// Two devices, three conditions: two checks, in the order the devices first appear: the same as BuildLabChecks makes.
	task := stateTask("ospf", "", a, b, a)
	require.Equal(t, []string{"ospf:1", "ospf:2"}, labCheckModel.ExpectedCheckIDs(task))
	require.Nil(t, labCheckModel.ExpectedCheckIDs(exerciseModel.Task{Key: "answer", Check: exerciseModel.FlagCheck()}))

	variant := exerciseModel.Variant{
		Tasks:    []exerciseModel.Task{task},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: a, Name: "r1", Type: exerciseModel.DeviceTypeNode}, {ID: b, Name: "r2", Type: exerciseModel.DeviceTypeNode}}},
	}
	checks, err := variant.BuildLabChecks(variant.Topology, []exerciseModel.ResolvedConditions{{TaskKey: "ospf", Conditions: task.Check.State.Conditions}})
	require.NoError(t, err)
	var made []string
	for _, c := range checks {
		made = append(made, c.ID)
	}
	require.Equal(t, labCheckModel.ExpectedCheckIDs(task), made, "the ids read back are the ids that were sent")
}

func TestTaskStateModeOnceStaysSolved(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variant := exerciseModel.Variant{Tasks: []exerciseModel.Task{stateTask("ospf", exerciseModel.StateModeOnce, a, b)}}

	// Nothing yet.
	states := labCheckModel.TaskStates(variant, nil)
	require.Len(t, states, 1)
	require.False(t, states[0].Solved)
	require.False(t, states[0].Known)

	// One of two checks passed: not solved.
	states = labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("ospf:1", labCheckModel.StatePassed, at(5), 5),
		result("ospf:2", labCheckModel.StateNotPassed, nil, 0),
	})
	require.False(t, states[0].Solved)
	require.True(t, states[0].Known)
	require.Nil(t, states[0].SolvedAt)

	// Both passed once: solved at the moment the last of them first passed.
	states = labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("ospf:1", labCheckModel.StatePassed, at(5), 5),
		result("ospf:2", labCheckModel.StatePassed, at(30), 30),
	})
	require.True(t, states[0].Solved)
	require.Equal(t, at(30).UTC(), states[0].SolvedAt.UTC())

	// The state goes back to not passed afterwards: mode once keeps the task solved, at the same time.
	states = labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("ospf:1", labCheckModel.StateNotPassed, at(5), 60),
		result("ospf:2", labCheckModel.StatePassed, at(30), 30),
	})
	require.True(t, states[0].Solved, "once: the first time observed, and it stays solved")
	require.Equal(t, at(30).UTC(), states[0].SolvedAt.UTC())
}

func TestTaskStateModeContinuousFollowsTheCurrentState(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variant := exerciseModel.Variant{Tasks: []exerciseModel.Task{stateTask("ospf", exerciseModel.StateModeContinuous, a, b)}}

	// Both pass now: solved, since the latest change.
	states := labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("ospf:1", labCheckModel.StatePassed, at(5), 5),
		result("ospf:2", labCheckModel.StatePassed, at(30), 30),
	})
	require.True(t, states[0].Solved)
	require.Equal(t, at(30).UTC(), states[0].SolvedAt.UTC())
	require.Equal(t, at(30).UTC(), states[0].FirstSolvedAt.UTC())

	// One goes back: not solved now, but the first time every check had passed is kept.
	states = labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("ospf:1", labCheckModel.StateNotPassed, at(5), 90),
		result("ospf:2", labCheckModel.StatePassed, at(30), 30),
	})
	require.False(t, states[0].Solved)
	require.Nil(t, states[0].SolvedAt)
	require.Equal(t, at(30).UTC(), states[0].FirstSolvedAt.UTC(), "the history stays")

	// It passes again: solved again, from the new change.
	states = labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("ospf:1", labCheckModel.StatePassed, at(5), 120),
		result("ospf:2", labCheckModel.StatePassed, at(30), 30),
	})
	require.True(t, states[0].Solved)
	require.Equal(t, at(120).UTC(), states[0].SolvedAt.UTC())
}

func TestTaskStatesIgnoreAnswerTasksAndForeignResults(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	variant := exerciseModel.Variant{Tasks: []exerciseModel.Task{
		{Key: "theory", Check: exerciseModel.TextCheck("OSPF")},
		stateTask("ospf", "", a),
	}}
	states := labCheckModel.TaskStates(variant, []labCheckModel.Result{
		result("other:1", labCheckModel.StatePassed, at(1), 1),
		result("theory:1", labCheckModel.StatePassed, at(1), 1),
	})
	require.Len(t, states, 1, "only the state tasks have a state")
	require.Equal(t, "ospf", states[0].Key)
	require.False(t, states[0].Solved)
	require.Equal(t, exerciseModel.StateModeOnce, states[0].Mode)
}
