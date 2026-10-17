package exerciseModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// ExportJob is a background export of tasks «з образами» (the archive carries the platform images of their
// devices, up to 10 GiB): read only by the user who started it. Running → done (the stored zip, read in chunks like
// any export) or failed (the error the user sees). It shares the states and the public error shape of ImportJob.
type ExportJob struct {
	ID          uuid.UUID
	CreatedBy   uuid.UUID
	ExerciseIDs []uuid.UUID
	// FileName is the name the stored zip gets.
	FileName string
	State    ImportJobState
	// FileID is the stored zip (done only).
	FileID uuid.UUID
	// Error is what the user is told when the export failed: never a secret or an internal detail.
	Error     *ImportJobError
	CreatedAt time.Time
	UpdatedAt time.Time
}

const (
	// ExportJobTimeout bounds one run of the export job: up to 10 GiB of images pulled and stored.
	ExportJobTimeout = 6 * time.Hour
	// ExportJobStaleAfter: a running export silent for longer than its job may run has stopped (a restart); it reads
	// as failed.
	ExportJobStaleAfter = ExportJobTimeout + 30*time.Minute
)

// NewExportJob starts an export of the tasks into a zip named fileName.
func NewExportJob(id, createdBy uuid.UUID, exerciseIDs []uuid.UUID, fileName string, now time.Time) ExportJob {
	return ExportJob{
		ID: id, CreatedBy: createdBy, ExerciseIDs: exerciseIDs, FileName: fileName,
		State: ImportJobRunning, CreatedAt: now, UpdatedAt: now,
	}
}

// OwnedBy tells whether user started the export.
func (j *ExportJob) OwnedBy(user uuid.UUID) bool { return user != uuid.Nil && j.CreatedBy == user }

// Current is the state as the user sees it now: a running export past ExportJobStaleAfter is failed
// (ErrArchiveExportInterrupted).
func (j *ExportJob) Current(now time.Time) (ImportJobState, *ImportJobError) {
	if j.State == ImportJobRunning && now.Sub(j.UpdatedAt) > ExportJobStaleAfter {
		e := ErrArchiveExportInterrupted.Err().StatusCode()
		return ImportJobFailed, &ImportJobError{Code: e.FullCode(), Message: e.Message()}
	}
	return j.State, j.Error
}

// Finish records the stored zip.
func (j *ExportJob) Finish(fileID uuid.UUID, now time.Time) {
	j.State, j.FileID, j.Error, j.UpdatedAt = ImportJobDone, fileID, nil, now
}

// Fail records the error the user is told.
func (j *ExportJob) Fail(failure ImportJobError, now time.Time) {
	j.State, j.Error, j.FileID, j.UpdatedAt = ImportJobFailed, &failure, uuid.Nil, now
}
