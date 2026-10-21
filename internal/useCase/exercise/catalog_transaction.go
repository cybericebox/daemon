package exercise

import (
	"context"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

// CatalogRepository is the existing aggregate port plus the few operations that
// must share its transaction. Uploads/registry I/O deliberately remain outside.
type CatalogRepository interface {
	IRepository
	GetUserByID(context.Context, uuid.UUID) (postgres.User, error)
	TryCreateImportedExercise(context.Context, postgres.TryCreateImportedExerciseParams) (postgres.Exercise, error)
	LockExerciseImportJob(context.Context, uuid.UUID) (postgres.ExerciseImportJob, error)
	LockExerciseForImport(context.Context, uuid.UUID) (uuid.UUID, error)
	LockExerciseImportUpload(context.Context, uuid.UUID) (postgres.MediaUpload, error)
	LockExerciseImportFile(context.Context, uuid.UUID) (postgres.File, error)
	ReplaceFileReferences(context.Context, postgres.ReplaceFileReferencesParams) error
	DeleteFileReferences(context.Context, postgres.DeleteFileReferencesParams) (int64, error)
	DeleteFileReferencesBatch(context.Context, postgres.DeleteFileReferencesBatchParams) (int64, error)
}

type catalogReferences struct{ q CatalogRepository }

func (m catalogReferences) ReplaceReferences(ctx context.Context, kind string, id uuid.UUID, ids []uuid.UUID) error {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return m.q.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: kind, RefID: id, FileIds: ids})
}
func (m catalogReferences) RemoveReferences(ctx context.Context, kind string, id uuid.UUID) error {
	_, err := m.q.DeleteFileReferences(ctx, postgres.DeleteFileReferencesParams{RefType: kind, RefID: id})
	return err
}
func (m catalogReferences) RemoveReferencesBatch(ctx context.Context, kind string, ids []uuid.UUID) error {
	_, err := m.q.DeleteFileReferencesBatch(ctx, postgres.DeleteFileReferencesBatchParams{RefType: kind, RefIds: ids})
	return err
}

// beginCatalog opens an explicitly bound local session; the shared use case is
// never rebound. Missing wiring is an error before mutation, never autocommit.
func (u *ExerciseUseCase) beginCatalog(ctx context.Context) (context.Context, *ExerciseUseCase, CatalogRepository, postgres.UoW, error) {
	if u.catalogUoW == nil {
		return ctx, nil, nil, nil, model.ErrPlatform.WithMessage("Catalog transaction is not wired").Err()
	}
	ctx, q, unit, err := u.catalogUoW.UnitOfWork(ctx)
	if err != nil {
		return ctx, nil, nil, nil, err
	}
	local := *u
	local.repo, local.exercises, local.media = q, exerciseRepo.New(q), catalogReferences{q}
	return ctx, &local, q, unit, nil
}
