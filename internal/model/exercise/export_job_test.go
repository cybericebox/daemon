package exerciseModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

var (
	exportTask1 = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000d1")
	exportFile  = uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000d2")
)

func TestExportJobLifecycle(t *testing.T) {
	j := exerciseModel.NewExportJob(jobID, jobUser, []uuid.UUID{exportTask1}, "web.cybericebox.zip", jobT0)
	require.Equal(t, exerciseModel.ImportJobRunning, j.State)
	require.Equal(t, []uuid.UUID{exportTask1}, j.ExerciseIDs)
	require.Equal(t, "web.cybericebox.zip", j.FileName)
	require.Equal(t, uuid.Nil, j.FileID)
	require.True(t, j.OwnedBy(jobUser))
	require.False(t, j.OwnedBy(uuid.Nil))
	require.False(t, j.OwnedBy(jobSource))

	j.Finish(exportFile, jobT0.Add(time.Minute))
	require.Equal(t, exerciseModel.ImportJobDone, j.State)
	require.Equal(t, exportFile, j.FileID)
	require.Nil(t, j.Error)
	require.Equal(t, jobT0.Add(time.Minute), j.UpdatedAt)

	failed := exerciseModel.NewExportJob(jobID, jobUser, []uuid.UUID{exportTask1}, "x.zip", jobT0)
	failure := exerciseModel.ImportJobError{Code: 72830, Message: "A platform image cannot be exported", Context: map[string]any{"device": "web"}}
	failed.Fail(failure, jobT0.Add(time.Minute))
	require.Equal(t, exerciseModel.ImportJobFailed, failed.State)
	require.Equal(t, &failure, failed.Error)
	require.Equal(t, uuid.Nil, failed.FileID)
}

// A running export silent for longer than its job may run reads as failed (interrupted).
func TestExportJobStaleReadsFailed(t *testing.T) {
	j := exerciseModel.NewExportJob(jobID, jobUser, []uuid.UUID{exportTask1}, "x.zip", jobT0)
	state, failure := j.Current(jobT0.Add(exerciseModel.ExportJobStaleAfter))
	require.Equal(t, exerciseModel.ImportJobRunning, state)
	require.Nil(t, failure)
	state, failure = j.Current(jobT0.Add(exerciseModel.ExportJobStaleAfter + time.Second))
	require.Equal(t, exerciseModel.ImportJobFailed, state)
	require.Equal(t, exerciseModel.ErrArchiveExportInterrupted.Err().StatusCode().FullCode(), failure.Code)
	require.Greater(t, exerciseModel.ExportJobStaleAfter, exerciseModel.ExportJobTimeout)

	j.Finish(exportFile, jobT0)
	state, failure = j.Current(jobT0.Add(48 * time.Hour))
	require.Equal(t, exerciseModel.ImportJobDone, state, "a finished export stays done")
	require.Nil(t, failure)
}
