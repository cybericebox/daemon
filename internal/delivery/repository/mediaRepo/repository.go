// Package mediaRepo is the repository for the media subsystem: logical files,
// content-addressed blobs and ownership references. sqlc/pgtype mapping stays
// here.
package mediaRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

type Queries interface {
	CreateFile(ctx context.Context, arg postgres.CreateFileParams) (postgres.File, error)
	GetFileByID(ctx context.Context, id uuid.UUID) (postgres.File, error)
	BlobExists(ctx context.Context, contentHash string) (bool, error)
	ReplaceFileReferences(ctx context.Context, arg postgres.ReplaceFileReferencesParams) error
	AddFileReference(ctx context.Context, arg postgres.AddFileReferenceParams) error
	GetFileReferenceIDs(ctx context.Context, arg postgres.GetFileReferenceIDsParams) ([]uuid.UUID, error)
	DeleteFileReferences(ctx context.Context, arg postgres.DeleteFileReferencesParams) (int64, error)
	DeleteFileReferencesBatch(ctx context.Context, arg postgres.DeleteFileReferencesBatchParams) (int64, error)
	DeleteUnreferencedFiles(ctx context.Context, createdBefore time.Time) ([]postgres.DeleteUnreferencedFilesRow, error)
	ListOrphanBlobs(ctx context.Context, touchedBefore time.Time) ([]string, error)
	DeleteBlob(ctx context.Context, contentHash string) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ── ownership references (file_references join table) ──

// ReplaceReferences syncs an owner's reference set to exactly fileIDs.
func (r *Repository) ReplaceReferences(ctx context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error {
	return r.q.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: refType, RefID: refID, FileIds: fileIDs})
}

func (r *Repository) AddReference(ctx context.Context, refType string, refID, fileID uuid.UUID) error {
	return r.q.AddFileReference(ctx, postgres.AddFileReferenceParams{RefType: refType, RefID: refID, FileID: fileID})
}

// ReferenceIDs returns the file ids referenced by (refType, refID).
func (r *Repository) ReferenceIDs(ctx context.Context, refType string, refID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: refType, RefID: refID})
}

// DeleteReferences drops every reference owned by refID.
func (r *Repository) DeleteReferences(ctx context.Context, refType string, refID uuid.UUID) (int64, error) {
	return r.q.DeleteFileReferences(ctx, postgres.DeleteFileReferencesParams{RefType: refType, RefID: refID})
}

// DeleteReferencesBatch drops references for a set of owners.
func (r *Repository) DeleteReferencesBatch(ctx context.Context, refType string, refIDs []uuid.UUID) (int64, error) {
	return r.q.DeleteFileReferencesBatch(ctx, postgres.DeleteFileReferencesBatchParams{RefType: refType, RefIds: refIDs})
}

// ── garbage collection ──

// DeleteUnreferenced removes files with no references older than the cutoff.
func (r *Repository) DeleteUnreferenced(ctx context.Context, createdBefore time.Time) error {
	_, err := r.q.DeleteUnreferencedFiles(ctx, createdBefore)
	return err
}

// ListOrphanBlobs returns content hashes of blobs no file references, older than
// the cutoff.
func (r *Repository) ListOrphanBlobs(ctx context.Context, touchedBefore time.Time) ([]string, error) {
	return r.q.ListOrphanBlobs(ctx, touchedBefore)
}

// DeleteBlob removes a blob by content hash.
func (r *Repository) DeleteBlob(ctx context.Context, contentHash string) (int64, error) {
	return r.q.DeleteBlob(ctx, contentHash)
}

func (r *Repository) CreateFile(ctx context.Context, f mediaModel.File) (mediaModel.File, error) {
	row, err := r.q.CreateFile(ctx, postgres.CreateFileParams{
		ID:          f.ID,
		Name:        f.Name,
		ContentType: f.ContentType,
		SizeBytes:   f.SizeBytes,
		ContentHash: f.ContentHash,
		CreatedAt:   f.CreatedAt,
		CreatedBy:   f.CreatedBy,
	})
	if err != nil {
		return mediaModel.File{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) GetFileByID(ctx context.Context, id uuid.UUID) (mediaModel.File, error) {
	row, err := r.q.GetFileByID(ctx, id)
	if err != nil {
		return mediaModel.File{}, err
	}
	return ToDomain(row), nil
}

func ToDomain(row postgres.File) mediaModel.File {
	return mediaModel.File{
		ID:          row.ID,
		Name:        row.Name,
		ContentType: row.ContentType,
		SizeBytes:   row.SizeBytes,
		ContentHash: row.ContentHash,
		CreatedAt:   row.CreatedAt,
		CreatedBy:   row.CreatedBy,
	}
}
