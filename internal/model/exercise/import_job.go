package exerciseModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// ImportJob is a background import of a task archive that carries device images (`image: {file: …}`): one per
// archive source, read only by the user who started it. Running → done (the created tasks) or failed (the error
// the user sees); a failed one may run again, a done one never.
type ImportJob struct {
	ID        uuid.UUID
	CreatedBy uuid.UUID
	Source    ImportSource
	SourceID  uuid.UUID
	State     ImportJobState
	// ExerciseIDs are the tasks the import created (done only).
	ExerciseIDs []uuid.UUID
	// Error is what the user is told when the import failed: never a secret or an internal detail.
	Error     *ImportJobError
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ImportSource is where the archive lies: a chunked upload (purpose exercise-archive) or a stored file.
type ImportSource string

const (
	ImportSourceUpload ImportSource = "upload"
	ImportSourceFile   ImportSource = "file"
)

type ImportJobState string

const (
	ImportJobRunning ImportJobState = "running"
	ImportJobDone    ImportJobState = "done"
	ImportJobFailed  ImportJobState = "failed"
)

// ImportJobError is the public part of the error a failed import ended with: the API code, message and public
// context, as an error response carries them.
type ImportJobError struct {
	Code    int            `json:"Code"`
	Message string         `json:"Message"`
	Context map[string]any `json:"Context,omitempty"`
}

const (
	// ImportJobTimeout bounds one run of the import job: up to 10 GiB of images pushed at a slow uplink.
	ImportJobTimeout = 6 * time.Hour
	// ImportJobStaleAfter: a running import silent for longer than its job may run has stopped (a restart); it
	// reads as failed and may run again.
	ImportJobStaleAfter = ImportJobTimeout + 30*time.Minute
)

// NewImportJob starts an import of the source.
func NewImportJob(id, createdBy, sourceID uuid.UUID, source ImportSource, now time.Time) ImportJob {
	return ImportJob{ID: id, CreatedBy: createdBy, Source: source, SourceID: sourceID, State: ImportJobRunning, CreatedAt: now, UpdatedAt: now}
}

// OwnedBy tells whether user started the import.
func (j *ImportJob) OwnedBy(user uuid.UUID) bool { return user != uuid.Nil && j.CreatedBy == user }

// Current is the state as the user sees it now: a running import past ImportJobStaleAfter is failed
// (ErrArchiveImportInterrupted).
func (j *ImportJob) Current(now time.Time) (ImportJobState, *ImportJobError) {
	if j.State == ImportJobRunning && now.Sub(j.UpdatedAt) > ImportJobStaleAfter {
		e := ErrArchiveImportInterrupted.Err().StatusCode()
		return ImportJobFailed, &ImportJobError{Code: e.FullCode(), Message: e.Message()}
	}
	return j.State, j.Error
}

// Restart runs a failed (or interrupted) import again; false when it is running or done.
func (j *ImportJob) Restart(now time.Time) bool {
	if state, _ := j.Current(now); state != ImportJobFailed {
		return false
	}
	j.State, j.Error, j.ExerciseIDs, j.UpdatedAt = ImportJobRunning, nil, nil, now
	return true
}

// Finish records the created tasks.
func (j *ImportJob) Finish(exerciseIDs []uuid.UUID, now time.Time) {
	j.State, j.ExerciseIDs, j.Error, j.UpdatedAt = ImportJobDone, exerciseIDs, nil, now
}

// Fail records the error the user is told.
func (j *ImportJob) Fail(failure ImportJobError, now time.Time) {
	j.State, j.Error, j.ExerciseIDs, j.UpdatedAt = ImportJobFailed, &failure, nil, now
}
