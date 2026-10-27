package event

import (
	"encoding/json"
	"strings"

	"github.com/gofrs/uuid"

	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// taskMatch is how the board challenges of a set meet the tasks of a new version of its exercise. A challenge
// keeps its row (id, group, order, points, publication, hint prices, the team copies) whenever a task of the new
// version is the same task: the same id, or, when a re-import re-issued the ids, the task with the same name.
type taskMatch struct {
	// Tasks is the new task of every challenge that stays, by challenge id.
	Tasks map[uuid.UUID]exerciseModel.Task
	// Repointed is the old task id of the challenges that stay but now point at a task with another id.
	Repointed map[uuid.UUID]uuid.UUID
	// Removed are the challenges whose task is gone; Added the tasks that have no challenge yet.
	Removed []uuid.UUID
	Added   []exerciseModel.Task
}

// matchBoardTasks pairs the board with the tasks of the canonical variant of the new version.
func matchBoardTasks(board []eventChallengeModel.EventChallenge, tasks []exerciseModel.Task) taskMatch {
	out := taskMatch{Tasks: make(map[uuid.UUID]exerciseModel.Task, len(board)), Repointed: map[uuid.UUID]uuid.UUID{}}
	taken := make(map[uuid.UUID]bool, len(tasks))
	byID := make(map[uuid.UUID]exerciseModel.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	var unmatched []int
	for i, challenge := range board {
		task, found := byID[challenge.TaskID]
		if !found {
			unmatched = append(unmatched, i)
			continue
		}
		out.Tasks[challenge.ID] = task
		taken[task.ID] = true
	}
	// The same name is the same task when the id changed: a re-import of the set re-issues the ids, and the
	// board must not lose its group, order, publication and team copies for it.
	freeByName := map[string][]exerciseModel.Task{}
	for _, task := range tasks {
		if !taken[task.ID] {
			name := normalizedTaskName(task.Name)
			freeByName[name] = append(freeByName[name], task)
		}
	}
	for _, i := range unmatched {
		challenge := board[i]
		name := normalizedTaskName(snapshotTaskName(challenge.Snapshot))
		candidates := freeByName[name]
		if name == "" || len(candidates) == 0 {
			out.Removed = append(out.Removed, challenge.ID)
			continue
		}
		task := candidates[0]
		freeByName[name] = candidates[1:]
		out.Tasks[challenge.ID] = task
		out.Repointed[challenge.ID] = challenge.TaskID
		taken[task.ID] = true
	}
	for _, task := range tasks {
		if !taken[task.ID] {
			out.Added = append(out.Added, task)
		}
	}
	return out
}

func normalizedTaskName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// snapshotTaskName reads the task name out of a board snapshot.
func snapshotTaskName(snapshot json.RawMessage) string {
	var value struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(snapshot, &value) != nil {
		return ""
	}
	return value.Name
}
