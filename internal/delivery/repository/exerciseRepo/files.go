package exerciseRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// StoredFile is the task's stored file for one content (query-side shape of the hash lookup).
type StoredFile struct {
	ID   uuid.UUID
	Name string
	Size int64
}

// FileByHash returns the task's stored file for a SHA-256; a task that holds no such content yields
// the not-found error of the query (repositoryTools.IsObjectNotFoundError).
func (r *Repository) FileByHash(ctx context.Context, exerciseID uuid.UUID, contentHash string) (StoredFile, error) {
	row, err := r.q.GetExerciseFileByHash(ctx, postgres.GetExerciseFileByHashParams{ExerciseID: exerciseID, ContentHash: contentHash})
	if err != nil {
		return StoredFile{}, err
	}
	return StoredFile{ID: row.ID, Name: row.Name, Size: row.SizeBytes}, nil
}

// ClaimFile records the file as the task's copy of its content and returns the task's canonical file for that
// content: the given one, or the one the task already held.
func (r *Repository) ClaimFile(ctx context.Context, exerciseID uuid.UUID, contentHash string, fileID uuid.UUID, now time.Time) (uuid.UUID, error) {
	return r.q.ClaimExerciseFile(ctx, postgres.ClaimExerciseFileParams{ExerciseID: exerciseID, ContentHash: contentHash, FileID: fileID, CreatedAt: now})
}

// FileHashes maps each existing file to its content hash; a file that does not exist is absent.
func (r *Repository) FileHashes(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := r.q.ListFileHashes(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.ContentHash
	}
	return out, nil
}

// HoldsFile reports whether the task stores the file as its copy of a content.
func (r *Repository) HoldsFile(ctx context.Context, exerciseID, fileID uuid.UUID) (bool, error) {
	return r.q.ExerciseHoldsFile(ctx, postgres.ExerciseHoldsFileParams{ExerciseID: exerciseID, FileID: fileID})
}
