package exerciseRepo

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// CreateExportJob records a new export job.
func (r *Repository) CreateExportJob(ctx context.Context, j exerciseModel.ExportJob) error {
	failure, err := importJobErrorJSON(j.Error)
	if err != nil {
		return err
	}
	return r.q.CreateExerciseExportJob(ctx, postgres.CreateExerciseExportJobParams{
		ID: j.ID, CreatedBy: j.CreatedBy, ExerciseIds: idsOrEmpty(j.ExerciseIDs), FileName: j.FileName, State: string(j.State),
		FileID: nullID(j.FileID), Error: failure, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
	})
}

// GetExportJob loads a job; an absent one is the query's not-found error.
func (r *Repository) GetExportJob(ctx context.Context, id uuid.UUID) (exerciseModel.ExportJob, error) {
	row, err := r.q.GetExerciseExportJob(ctx, id)
	if err != nil {
		return exerciseModel.ExportJob{}, err
	}
	j := exerciseModel.ExportJob{
		ID: row.ID, CreatedBy: row.CreatedBy, ExerciseIDs: row.ExerciseIds, FileName: row.FileName,
		State: exerciseModel.ImportJobState(row.State), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.FileID.Valid {
		j.FileID = row.FileID.UUID
	}
	j.Error = importJobFromRow(postgres.ExerciseImportJob{Error: row.Error}).Error
	return j, nil
}

// SaveExportJob writes the whole job (state, stored file, error); it reports how many rows changed.
func (r *Repository) SaveExportJob(ctx context.Context, j exerciseModel.ExportJob) (int64, error) {
	failure, err := importJobErrorJSON(j.Error)
	if err != nil {
		return 0, err
	}
	return r.q.UpdateExerciseExportJob(ctx, postgres.UpdateExerciseExportJobParams{
		ID: j.ID, State: string(j.State), FileID: nullID(j.FileID), Error: failure, UpdatedAt: j.UpdatedAt,
	})
}

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }
