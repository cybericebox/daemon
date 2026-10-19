package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
)

// MaxBulkExercises bounds one bulk action on the event's exercises.
const MaxBulkExercises = 200

// BulkExerciseResult is one exercise of a bulk action: Err nil means it was applied, else why it was skipped.
type BulkExerciseResult struct {
	ID  uuid.UUID
	Err error
}

// bulkExerciseIDs drops repeated IDs (first wins) and checks the size: 1..MaxBulkExercises.
func bulkExerciseIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok || id == uuid.Nil {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 || len(out) > MaxBulkExercises {
		return nil, eventExerciseModel.ErrEventExerciseBulkSize.Err()
	}
	return out, nil
}

// eachExercise runs the single-item action for every exercise in order. One failure never stops the others;
// a cancelled request stops before the next item and reports the rest as skipped with that error.
func eachExercise(ctx context.Context, ids []uuid.UUID, apply func(uuid.UUID) error) ([]BulkExerciseResult, error) {
	ids, err := bulkExerciseIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make([]BulkExerciseResult, 0, len(ids))
	for _, id := range ids {
		if ctxErr := ctx.Err(); ctxErr != nil {
			out = append(out, BulkExerciseResult{ID: id, Err: model.ErrPlatform.WithError(ctxErr).WithMessage("Request cancelled").Err()})
			continue
		}
		out = append(out, BulkExerciseResult{ID: id, Err: apply(id)})
	}
	return out, nil
}

// BulkSetEventExerciseVisibility shows or hides every selected set, with the single action's rules per set.
func (u *EventUseCase) BulkSetEventExerciseVisibility(ctx context.Context, eventID uuid.UUID, ids []uuid.UUID, published bool) ([]BulkExerciseResult, error) {
	return eachExercise(ctx, ids, func(id uuid.UUID) error {
		return u.SetEventExerciseVisibility(ctx, eventID, id, published)
	})
}

// BulkSetEventExerciseScoring gives every task of each selected set the same local scoring (nil: the event's),
// with the single set action's ownership and lifecycle checks per set.
func (u *EventUseCase) BulkSetEventExerciseScoring(ctx context.Context, eventID uuid.UUID, ids []uuid.UUID, override *eventModel.ScoringProfile) ([]BulkExerciseResult, error) {
	return eachExercise(ctx, ids, func(id uuid.UUID) error {
		board, err := u.eventChallenges.List(ctx, id)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
		}
		challengeIDs := make([]uuid.UUID, 0, len(board))
		for _, challenge := range board {
			challengeIDs = append(challengeIDs, challenge.ID)
		}
		in := BulkUpdateChallengeScoringInput{ChallengeIDs: challengeIDs}
		if override != nil {
			profile := *override
			in.Override = &profile
		}
		return u.BulkUpdateChallengeScoring(ctx, eventID, id, in)
	})
}

// BulkDetachEventExercises removes every selected set with the single detach rules: without confirmation a set
// teams already attempted is skipped; a set whose stage has opened stays.
func (u *EventUseCase) BulkDetachEventExercises(ctx context.Context, eventID uuid.UUID, ids []uuid.UUID, confirmed bool, by uuid.UUID) ([]BulkExerciseResult, error) {
	return eachExercise(ctx, ids, func(id uuid.UUID) error {
		return u.DetachEventExercise(ctx, eventID, id, confirmed, by)
	})
}
