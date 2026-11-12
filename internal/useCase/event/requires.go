package event

import (
	"context"
	"slices"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The `requires` of a task of the document ("this task opens after these tasks are done") becomes the prerequisites of its
// event challenge, the board's own lock: a team's challenge with an unsolved prerequisite is locked (the board shows its name
// and difficulty and the names of the prerequisites it waits for, and files stay hidden) and accepts no answer and no manual
// check. The organizer may change the relations afterwards, like any other: the document sets them when the task joins the
// event and when a newer version changes a task's `requires`.

// applyDocumentRequires gives the event challenges of the tasks that have `requires` the prerequisites the document names:
// the challenge of every required task, found by key. wanted picks the tasks to do (nil: every task that has requires); a task
// it picks with no requires gets none (the document dropped them). A key that names no challenge is skipped.
func applyDocumentRequires(ctx context.Context, challenges *eventChallengeRepo.Repository, eventExerciseID uuid.UUID, tasks []exerciseModel.Task, wanted func(exerciseModel.Task) bool) error {
	todo := false
	for _, task := range tasks {
		if (wanted == nil && len(task.Requires) > 0) || (wanted != nil && wanted(task)) {
			todo = true
		}
	}
	if !todo {
		return nil // nothing to set: no read of the board
	}
	board, err := challenges.List(ctx, eventExerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	challengeOfTask := make(map[uuid.UUID]uuid.UUID, len(board))
	for _, challenge := range board {
		challengeOfTask[challenge.TaskID] = challenge.ID
	}
	challengeOfKey := make(map[string]uuid.UUID, len(tasks))
	for _, task := range tasks {
		if id, ok := challengeOfTask[task.ID]; ok {
			challengeOfKey[task.Key] = id
		}
	}
	for _, task := range tasks {
		if wanted == nil && len(task.Requires) == 0 {
			continue
		}
		if wanted != nil && !wanted(task) {
			continue
		}
		own, ok := challengeOfTask[task.ID]
		if !ok {
			continue
		}
		prerequisites := make([]uuid.UUID, 0, len(task.Requires))
		for _, key := range task.Requires {
			if id, found := challengeOfKey[key]; found && id != own && !slices.Contains(prerequisites, id) {
				prerequisites = append(prerequisites, id)
			}
		}
		if err = challenges.ReplacePrerequisites(ctx, own, prerequisites); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to set the prerequisites of an event challenge").Err()
		}
	}
	return nil
}

// requiresChanged reports whether a task asks for other tasks than it did in the version before (compared as sets of keys).
func requiresChanged(previous, next []string) bool {
	a, b := slices.Clone(previous), slices.Clone(next)
	slices.Sort(a)
	slices.Sort(b)
	return !slices.Equal(a, b)
}

// previousRequires maps every challenge of the board to the keys its task required in the version that was pinned until now.
func previousRequires(board []challengeRef, previous exerciseModel.ExerciseVersion, canonical int) map[uuid.UUID][]string {
	out := map[uuid.UUID][]string{}
	if canonical < 0 || canonical >= len(previous.Variants) {
		return out
	}
	byID := make(map[uuid.UUID]exerciseModel.Task, len(previous.Variants[canonical].Tasks))
	for _, task := range previous.Variants[canonical].Tasks {
		byID[task.ID] = task
	}
	for _, challenge := range board {
		if task, ok := byID[challenge.TaskID]; ok {
			out[challenge.ID] = task.Requires
		}
	}
	return out
}

// challengeRef is the part of an event challenge the requires bookkeeping needs.
type challengeRef struct{ ID, TaskID uuid.UUID }
