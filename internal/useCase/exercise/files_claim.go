package exercise

import (
	"context"
	"slices"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// requireFilesClaimable refuses an attachment the caller has no claim on. A file may be attached to an
// exercise draft by its uploader, or when the exercise already holds it (rolling back to a version, restoring
// one, editing together). A file of someone else, named by its id, reads as not found: its id is not a
// capability, and a draft that references a file makes it downloadable to everyone who may read the draft.
func (u *ExerciseUseCase) requireFilesClaimable(ctx context.Context, exerciseID, by uuid.UUID, files []uuid.UUID) error {
	if len(files) == 0 {
		return nil
	}
	unique := slices.Clone(files)
	slices.SortFunc(unique, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	unique = slices.Compact(unique)
	owners, err := u.exercises.FileOwners(ctx, unique)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check attached files").Err()
	}
	for _, id := range unique {
		owner, exists := owners[id]
		if !exists {
			return mediaModel.ErrFileNotFound.Err()
		}
		if owner.Valid && by != uuid.Nil && owner.UUID == by {
			continue
		}
		holders, holdErr := u.exercises.FileExerciseIDs(ctx, id, mediaModel.RefTypeExerciseVersion)
		if holdErr != nil {
			return model.ErrPlatform.WithError(holdErr).WithMessage("Failed to check attached files").Err()
		}
		if !slices.Contains(holders, exerciseID) {
			return mediaModel.ErrFileNotFound.Err()
		}
	}
	return nil
}
