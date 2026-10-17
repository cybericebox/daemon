package exerciseModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

var (
	jobT0     = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	jobID     = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000b1")
	jobUser   = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000b2")
	jobSource = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000b3")
)

func TestImportJobLifecycle(t *testing.T) {
	j := exerciseModel.NewImportJob(jobID, jobUser, jobSource, exerciseModel.ImportSourceUpload, jobT0)
	require.Equal(t, exerciseModel.ImportJobRunning, j.State)
	require.True(t, j.OwnedBy(jobUser))
	require.False(t, j.OwnedBy(uuid.Nil))

	failure := exerciseModel.ImportJobError{Code: 20982, Message: "The archive holds a file nothing links to", Context: map[string]any{"file": "x"}}
	j.Fail(failure, jobT0.Add(time.Minute))
	require.Equal(t, exerciseModel.ImportJobFailed, j.State)
	require.Equal(t, &failure, j.Error)
	require.Equal(t, jobT0.Add(time.Minute), j.UpdatedAt)

	require.True(t, j.Restart(jobT0.Add(2*time.Minute)), "a failed import may run again")
	require.Equal(t, exerciseModel.ImportJobRunning, j.State)
	require.Nil(t, j.Error)
	require.False(t, j.Restart(jobT0.Add(3*time.Minute)), "a running import is not started twice")

	created := []uuid.UUID{uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000c1")}
	j.Finish(created, jobT0.Add(4*time.Minute))
	require.Equal(t, exerciseModel.ImportJobDone, j.State)
	require.Equal(t, created, j.ExerciseIDs)
	require.False(t, j.Restart(jobT0.Add(5*time.Minute)), "a finished import is never run again")
}

// A running import silent for longer than its job may run reads as failed (interrupted).
func TestImportJobStaleReadsFailed(t *testing.T) {
	j := exerciseModel.NewImportJob(jobID, jobUser, jobSource, exerciseModel.ImportSourceFile, jobT0)
	state, failure := j.Current(jobT0.Add(exerciseModel.ImportJobStaleAfter))
	require.Equal(t, exerciseModel.ImportJobRunning, state)
	require.Nil(t, failure)
	state, failure = j.Current(jobT0.Add(exerciseModel.ImportJobStaleAfter + time.Second))
	require.Equal(t, exerciseModel.ImportJobFailed, state)
	require.NotNil(t, failure)
	require.Equal(t, exerciseModel.ErrArchiveImportInterrupted.Err().StatusCode().FullCode(), failure.Code)
	require.Greater(t, exerciseModel.ImportJobStaleAfter, exerciseModel.ImportJobTimeout)
	require.True(t, j.Restart(jobT0.Add(exerciseModel.ImportJobStaleAfter+time.Second)), "an interrupted import may run again")
}
