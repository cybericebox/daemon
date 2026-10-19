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
	// OwnerEventID is the event the tasks are created in (an event moderator's import); NULL: the platform catalog.
	OwnerEventID uuid.NullUUID
	State        ImportJobState
	// ExerciseIDs are the tasks the import created (done only).
	ExerciseIDs []uuid.UUID
	// Error is what the user is told when the import failed: never a secret or an internal detail.
	Error *ImportJobError
	// Progress is how far a running import has come (the stage and the counts the user sees).
	Progress  ImportProgress
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

// ImportStage is the step a running import is in; done and failed are the job states, reported as stages too.
type ImportStage string

const (
	ImportStageUnpacking ImportStage = "unpacking"
	ImportStageImages    ImportStage = "images"
	ImportStageTasks     ImportStage = "tasks"
	ImportStageDone      ImportStage = "done"
	ImportStageFailed    ImportStage = "failed"
)

// ImportProgress is the progress of a running import: the stage and what is done of what is known so far (a total
// is 0 until the stage that learns it). Bytes are the image bytes pushed to the registry, counted per finished image.
type ImportProgress struct {
	Stage       ImportStage
	ImagesDone  int
	ImagesTotal int
	TasksDone   int
	TasksTotal  int
	BytesPushed int64
	BytesTotal  int64
}

const (
	// ImportJobTimeout bounds one run of the import job: up to 10 GiB of images pushed at a slow uplink.
	ImportJobTimeout = 6 * time.Hour
	// ImportJobStaleAfter: a running import silent for longer than its job may run has stopped (a restart); it
	// reads as failed and may run again.
	ImportJobStaleAfter = ImportJobTimeout + 30*time.Minute
)

// NewImportJob starts an import of the source.
func NewImportJob(id, createdBy, sourceID uuid.UUID, source ImportSource, ownerEventID uuid.NullUUID, now time.Time) ImportJob {
	return ImportJob{ID: id, CreatedBy: createdBy, Source: source, SourceID: sourceID, OwnerEventID: ownerEventID, State: ImportJobRunning, CreatedAt: now, UpdatedAt: now}
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

// Restart runs a failed (or interrupted) import again, into ownerEventID (the target of the new request); false when
// it is running or done.
func (j *ImportJob) Restart(ownerEventID uuid.NullUUID, now time.Time) bool {
	if state, _ := j.Current(now); state != ImportJobFailed {
		return false
	}
	j.OwnerEventID = ownerEventID
	j.State, j.Error, j.ExerciseIDs, j.Progress, j.UpdatedAt = ImportJobRunning, nil, nil, ImportProgress{Stage: ImportStageUnpacking}, now
	return true
}

// Report records the progress of a running import; a finished one keeps its outcome (false).
func (j *ImportJob) Report(p ImportProgress, now time.Time) bool {
	if j.State != ImportJobRunning {
		return false
	}
	j.Progress, j.UpdatedAt = p, now
	return true
}

// Stage is the stage as the user sees it now: done or failed once the import has ended (an interrupted one is
// failed), otherwise the running stage (unpacking before any progress is recorded).
func (j *ImportJob) Stage(now time.Time) ImportStage {
	switch state, _ := j.Current(now); state {
	case ImportJobDone:
		return ImportStageDone
	case ImportJobFailed:
		return ImportStageFailed
	}
	if j.Progress.Stage == "" {
		return ImportStageUnpacking
	}
	return j.Progress.Stage
}

// Finish records the created tasks.
func (j *ImportJob) Finish(exerciseIDs []uuid.UUID, now time.Time) {
	j.State, j.ExerciseIDs, j.Error, j.UpdatedAt = ImportJobDone, exerciseIDs, nil, now
	j.Progress.Stage = ImportStageDone
	j.Progress.ImagesDone, j.Progress.TasksDone, j.Progress.BytesPushed = j.Progress.ImagesTotal, len(exerciseIDs), j.Progress.BytesTotal
	if j.Progress.TasksTotal < len(exerciseIDs) {
		j.Progress.TasksTotal = len(exerciseIDs)
	}
}

// Fail records the error the user is told.
func (j *ImportJob) Fail(failure ImportJobError, now time.Time) {
	j.State, j.Error, j.ExerciseIDs, j.UpdatedAt = ImportJobFailed, &failure, nil, now
	j.Progress.Stage = ImportStageFailed
}
