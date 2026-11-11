// Package labCheckModel is what the platform records of the checks a laboratory runs for the state tasks of a lab: the
// result of each check by its id, and how the results of a task's checks make the task solved.
package labCheckModel

import (
	"slices"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// State is the last known state of one check. A check without a result is not in the list at all.
type State int16

const (
	StatePassed    State = 1
	StateNotPassed State = 2
)

// Result is the stored result of one check of a lab. Detail and Value are for staff (the task author, the teacher):
// they may hold what the participant must find themselves, so they never reach a participant.
type Result struct {
	Group, Lab, CheckID string
	State               State
	// FirstPassedAt is the first time a pass was seen; nil while the check never passed. It never goes back.
	FirstPassedAt *time.Time
	// ChangedAt is when the state last changed.
	ChangedAt  time.Time
	Value      string
	Detail     string
	ObservedAt time.Time
}

// TaskState is what the results say about one state task of a lab.
type TaskState struct {
	Key string
	// Mode is the mode of the task's check.
	Mode exerciseModel.StateMode
	// Solved: mode once: every check of the task has passed at least once, and it stays so; mode continuous: every check passes
	// now, and it may go back to false.
	Solved bool
	// SolvedAt is when the task became solved: once: the moment the last of its checks first passed; continuous: the moment the
	// current pass began (the latest change among its checks). nil while not solved.
	SolvedAt *time.Time
	// FirstSolvedAt is the first moment every check of the task had passed at least once (the same as SolvedAt for once), nil
	// while that never happened. It is what a continuous task keeps after it went back to not solved.
	FirstSolvedAt *time.Time
	// Known: every check of the task has a result.
	Known bool
}

// ExpectedCheckIDs are the ids of the laboratory checks of a state task: one per device its conditions look at, in order of
// first appearance, as BuildLabChecks makes them. Without resolving anything, so the ids can be derived whenever the variant is.
func ExpectedCheckIDs(task exerciseModel.Task) []string {
	if task.Check.State == nil {
		return nil
	}
	var devices []string
	for _, c := range task.Check.State.Conditions {
		id := c.DeviceID.String()
		if !slices.Contains(devices, id) {
			devices = append(devices, id)
		}
	}
	ids := make([]string, len(devices))
	for i := range devices {
		ids[i] = exerciseModel.CheckID(task.Key, i+1)
	}
	return ids
}

// TaskStates maps the results of a lab back to the state tasks of its variant. A task none of whose checks has a result is
// present with Known false.
func TaskStates(variant exerciseModel.Variant, results []Result) []TaskState {
	byID := make(map[string]Result, len(results))
	for _, r := range results {
		byID[r.CheckID] = r
	}
	var out []TaskState
	for _, task := range variant.StateTasks() {
		mode := task.Check.State.Mode
		if mode == "" {
			mode = exerciseModel.StateModeOnce
		}
		state := TaskState{Key: task.Key, Mode: mode, Known: true}
		ids := ExpectedCheckIDs(task)
		var latestChange, latestFirst time.Time
		allNow, allEver := len(ids) > 0, len(ids) > 0
		for _, id := range ids {
			r, ok := byID[id]
			if !ok {
				state.Known, allNow, allEver = false, false, false
				continue
			}
			if r.State != StatePassed {
				allNow = false
			}
			if r.FirstPassedAt == nil {
				allEver = false
			} else if r.FirstPassedAt.After(latestFirst) {
				latestFirst = *r.FirstPassedAt
			}
			if r.ChangedAt.After(latestChange) {
				latestChange = r.ChangedAt
			}
		}
		if allEver {
			first := latestFirst
			state.FirstSolvedAt = &first
		}
		switch mode {
		case exerciseModel.StateModeContinuous:
			if allNow {
				at := latestChange
				state.Solved, state.SolvedAt = true, &at
			}
		case exerciseModel.StateModeOnce:
			if allEver {
				state.Solved, state.SolvedAt = true, state.FirstSolvedAt
			}
		}
		out = append(out, state)
	}
	return out
}

// ChecksRunRateLimit bounds the manual runs of one task's checks: at most Attempts within Window per lab group (a team) and task.
var ChecksRunRateLimit = struct {
	Attempts int
	Window   time.Duration
}{Attempts: 3, Window: time.Minute}
