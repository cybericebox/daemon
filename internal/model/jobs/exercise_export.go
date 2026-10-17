package jobsModel

import (
	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"
)

// ExerciseExportArgs runs one background export of tasks «з образами» (the exercise_export_jobs row JobID). Unique
// per job while queued or running. IncludeSecrets writes secret values into a password-protected archive; the
// password is sealed with the platform cipher (bound to the job id), "" without secrets: the plain password is never
// stored.
type ExerciseExportArgs struct {
	JobID          uuid.UUID `json:"job_id" river:"unique"`
	IncludeSecrets bool      `json:"include_secrets,omitempty"`
	SealedPassword string    `json:"sealed_password,omitempty"`
}

func (ExerciseExportArgs) Kind() string { return "exercise_export" }

// InsertOpts: the export records its own outcome; only a failure to write that state is returned to River, and a
// retry of a multi-GiB export should not run for long. It runs on ImagesQueue.
func (ExerciseExportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, Queue: ImagesQueue}
}
