package media

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

// ReplaceReferences syncs the owner's reference set to exactly fileIDs.
func (u *MediaUseCase) ReplaceReferences(ctx context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error {
	if fileIDs == nil {
		fileIDs = []uuid.UUID{}
	}
	if err := u.files.ReplaceReferences(ctx, refType, refID, fileIDs); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to replace file references").Err()
	}
	return nil
}

// AddReference attaches one uploaded file without replacing links from other
// concurrent uploads to the same draft.
func (u *MediaUseCase) AddReference(ctx context.Context, refType string, refID, fileID uuid.UUID) error {
	if err := u.files.AddReference(ctx, refType, refID, fileID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to add file reference").Err()
	}
	return nil
}

// GetReferences returns the file IDs currently referenced by (refType, refID).
// Repositories don't validate on read: an owner with no references yields an
// empty slice, not an error.
func (u *MediaUseCase) GetReferences(ctx context.Context, refType string, refID uuid.UUID) ([]uuid.UUID, error) {
	fileIDs, err := u.files.ReferenceIDs(ctx, refType, refID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get file references").Err()
	}
	return fileIDs, nil
}

// RemoveReferences drops every reference owned by refID.
func (u *MediaUseCase) RemoveReferences(ctx context.Context, refType string, refID uuid.UUID) error {
	if _, err := u.files.DeleteReferences(ctx, refType, refID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove file references").Err()
	}
	return nil
}

// RemoveReferencesBatch drops references for a set of owners (exercise delete
// removes all its versions' references in one call).
func (u *MediaUseCase) RemoveReferencesBatch(ctx context.Context, refType string, refIDs []uuid.UUID) error {
	if len(refIDs) == 0 {
		return nil
	}
	if _, err := u.files.DeleteReferencesBatch(ctx, refType, refIDs); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove file references batch").Err()
	}
	return nil
}
