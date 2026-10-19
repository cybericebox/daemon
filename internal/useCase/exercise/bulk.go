package exercise

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

// MaxBulkExercises caps the tasks of one bulk request of the catalog table. The routes refuse more with 400
// (binding max on IDs, kept equal to this); bulk() refuses more itself, so no caller can run a bigger batch.
const MaxBulkExercises = 200

// BulkResult is the outcome of one task of a bulk action: the changed card, or the error that skipped it.
type BulkResult struct {
	ID   uuid.UUID
	View ExerciseView
	Err  error
}

// BulkSetArchived archives (or returns to the catalog) every task of ids by the single-task rule: the exercise
// write right (AuthorizeExercise) and then ArchiveExercise / UnarchiveExercise. A refused or failed task is
// reported and the rest go on; a repeated id is handled once. More than MaxBulkExercises distinct ids change nothing.
func (u *ExerciseUseCase) BulkSetArchived(ctx context.Context, actor Actor, ids []uuid.UUID, archived bool) ([]BulkResult, error) {
	apply := u.UnarchiveExercise
	if archived {
		apply = u.ArchiveExercise
	}
	return u.bulk(ids, func(id uuid.UUID) (ExerciseView, error) {
		if _, err := u.AuthorizeExercise(ctx, actor, id, ActionWrite); err != nil {
			return ExerciseView{}, err
		}
		return apply(ctx, id, actor.UserID)
	})
}

// BulkSetAccess sets the same access on every task of ids through SetExerciseAccess (route gate: exercises.write,
// as for one task). An event task or an invalid level for a task is reported and the rest go on.
func (u *ExerciseUseCase) BulkSetAccess(ctx context.Context, actor Actor, ids []uuid.UUID, in SetAccessInput) ([]BulkResult, error) {
	return u.bulk(ids, func(id uuid.UUID) (ExerciseView, error) {
		return u.SetExerciseAccess(ctx, actor, id, in)
	})
}

func (u *ExerciseUseCase) bulk(ids []uuid.UUID, apply func(uuid.UUID) (ExerciseView, error)) ([]BulkResult, error) {
	ids = uniqueIDs(ids)
	if len(ids) > MaxBulkExercises {
		// The routes cap the batch first; reaching here is a caller bug, not user input.
		return nil, model.ErrPlatform.WithMessage(fmt.Sprintf("at most %d exercises per bulk action, got %d", MaxBulkExercises, len(ids))).Err()
	}
	out := make([]BulkResult, 0, len(ids))
	for _, id := range ids {
		view, err := apply(id)
		out = append(out, BulkResult{ID: id, View: view, Err: err})
	}
	return out, nil
}
