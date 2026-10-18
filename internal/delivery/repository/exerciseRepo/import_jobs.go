package exerciseRepo

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// StartImportJob records a new import job, or returns the job its source already has (one per source).
func (r *Repository) StartImportJob(ctx context.Context, j exerciseModel.ImportJob) (exerciseModel.ImportJob, error) {
	failure, err := importJobErrorJSON(j.Error)
	if err != nil {
		return exerciseModel.ImportJob{}, err
	}
	row, err := r.q.StartExerciseImportJob(ctx, postgres.StartExerciseImportJobParams{
		ID: j.ID, CreatedBy: j.CreatedBy, Source: string(j.Source), SourceID: j.SourceID, State: string(j.State),
		ExerciseIds: idsOrEmpty(j.ExerciseIDs), Error: failure, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
	})
	if err != nil {
		return exerciseModel.ImportJob{}, err
	}
	return importJobFromRow(row), nil
}

// GetImportJob loads a job; an absent one is the query's not-found error.
func (r *Repository) GetImportJob(ctx context.Context, id uuid.UUID) (exerciseModel.ImportJob, error) {
	row, err := r.q.GetExerciseImportJob(ctx, id)
	if err != nil {
		return exerciseModel.ImportJob{}, err
	}
	return importJobFromRow(row), nil
}

// SaveImportJob writes the whole job (state, created tasks, error); it reports how many rows changed.
func (r *Repository) SaveImportJob(ctx context.Context, j exerciseModel.ImportJob) (int64, error) {
	failure, err := importJobErrorJSON(j.Error)
	if err != nil {
		return 0, err
	}
	return r.q.UpdateExerciseImportJob(ctx, postgres.UpdateExerciseImportJobParams{
		ID: j.ID, State: string(j.State), ExerciseIds: idsOrEmpty(j.ExerciseIDs), Error: failure, UpdatedAt: j.UpdatedAt,
		Stage: stageOrDefault(j.Progress.Stage), ImagesDone: int32(j.Progress.ImagesDone), ImagesTotal: int32(j.Progress.ImagesTotal),
		TasksDone: int32(j.Progress.TasksDone), TasksTotal: int32(j.Progress.TasksTotal),
		BytesPushed: j.Progress.BytesPushed, BytesTotal: j.Progress.BytesTotal,
	})
}

// SaveImportJobProgress writes only the progress of a running job; zero rows: the job has ended or is gone.
func (r *Repository) SaveImportJobProgress(ctx context.Context, j exerciseModel.ImportJob) (int64, error) {
	return r.q.UpdateExerciseImportJobProgress(ctx, postgres.UpdateExerciseImportJobProgressParams{
		ID: j.ID, Stage: stageOrDefault(j.Progress.Stage), ImagesDone: int32(j.Progress.ImagesDone), ImagesTotal: int32(j.Progress.ImagesTotal),
		TasksDone: int32(j.Progress.TasksDone), TasksTotal: int32(j.Progress.TasksTotal),
		BytesPushed: j.Progress.BytesPushed, BytesTotal: j.Progress.BytesTotal, UpdatedAt: j.UpdatedAt,
	})
}

func stageOrDefault(stage exerciseModel.ImportStage) string {
	if stage == "" {
		return string(exerciseModel.ImportStageUnpacking)
	}
	return string(stage)
}

func idsOrEmpty(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func importJobErrorJSON(e *exerciseModel.ImportJobError) ([]byte, error) {
	if e == nil {
		return nil, nil
	}
	return json.Marshal(e)
}

// importJobFromRow never refuses a row: an error that does not decode is kept as its code-less shape.
func importJobFromRow(row postgres.ExerciseImportJob) exerciseModel.ImportJob {
	j := exerciseModel.ImportJob{
		ID: row.ID, CreatedBy: row.CreatedBy, Source: exerciseModel.ImportSource(row.Source), SourceID: row.SourceID,
		State: exerciseModel.ImportJobState(row.State), ExerciseIDs: row.ExerciseIds, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Progress: exerciseModel.ImportProgress{
			Stage: exerciseModel.ImportStage(row.Stage), ImagesDone: int(row.ImagesDone), ImagesTotal: int(row.ImagesTotal),
			TasksDone: int(row.TasksDone), TasksTotal: int(row.TasksTotal), BytesPushed: row.BytesPushed, BytesTotal: row.BytesTotal,
		},
	}
	if len(row.Error) > 0 {
		var e exerciseModel.ImportJobError
		_ = json.Unmarshal(row.Error, &e)
		j.Error = &e
	}
	return j
}
