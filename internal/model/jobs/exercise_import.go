package jobsModel

import (
	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"
)

// ExerciseImportArgs runs one background import of a task archive with device images (the exercise_import_jobs
// row JobID). Unique per job while queued or running. SealedPassword is the archive password sealed with the
// platform cipher (bound to the job id), "" for an unprotected archive: the plain password is never stored.
type ExerciseImportArgs struct {
	JobID          uuid.UUID `json:"job_id" river:"unique"`
	SealedPassword string    `json:"sealed_password,omitempty"`
}

func (ExerciseImportArgs) Kind() string { return "exercise_import" }

// InsertOpts: the import records its own failure; only a failure to write that state is returned to River, so a
// few attempts are enough.
func (ExerciseImportArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }
