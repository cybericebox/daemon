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
	j := exerciseModel.NewImportJob(jobID, jobUser, jobSource, exerciseModel.ImportSourceUpload, uuid.NullUUID{}, jobT0)
	require.Equal(t, exerciseModel.ImportJobRunning, j.State)
	require.True(t, j.OwnedBy(jobUser))
	require.False(t, j.OwnedBy(uuid.Nil))

	failure := exerciseModel.ImportJobError{Code: 20982, Message: "The archive holds a file nothing links to", Context: map[string]any{"file": "x"}}
	j.Fail(failure, jobT0.Add(time.Minute))
	require.Equal(t, exerciseModel.ImportJobFailed, j.State)
	require.Equal(t, &failure, j.Error)
	require.Equal(t, jobT0.Add(time.Minute), j.UpdatedAt)

	require.True(t, j.Restart(uuid.NullUUID{}, jobT0.Add(2*time.Minute)), "a failed import may run again")
	require.Equal(t, exerciseModel.ImportJobRunning, j.State)
	require.Nil(t, j.Error)
	require.False(t, j.Restart(uuid.NullUUID{}, jobT0.Add(3*time.Minute)), "a running import is not started twice")

	created := []uuid.UUID{uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000c1")}
	j.Finish(created, jobT0.Add(4*time.Minute))
	require.Equal(t, exerciseModel.ImportJobDone, j.State)
	require.Equal(t, created, j.ExerciseIDs)
	require.False(t, j.Restart(uuid.NullUUID{}, jobT0.Add(5*time.Minute)), "a finished import is never run again")
}

// A running import silent for longer than its job may run reads as failed (interrupted).
func TestImportJobStaleReadsFailed(t *testing.T) {
	j := exerciseModel.NewImportJob(jobID, jobUser, jobSource, exerciseModel.ImportSourceFile, uuid.NullUUID{}, jobT0)
	state, failure := j.Current(jobT0.Add(exerciseModel.ImportJobStaleAfter))
	require.Equal(t, exerciseModel.ImportJobRunning, state)
	require.Nil(t, failure)
	state, failure = j.Current(jobT0.Add(exerciseModel.ImportJobStaleAfter + time.Second))
	require.Equal(t, exerciseModel.ImportJobFailed, state)
	require.NotNil(t, failure)
	require.Equal(t, exerciseModel.ErrArchiveImportInterrupted.Err().StatusCode().FullCode(), failure.Code)
	require.Greater(t, exerciseModel.ImportJobStaleAfter, exerciseModel.ImportJobTimeout)
	require.True(t, j.Restart(uuid.NullUUID{}, jobT0.Add(exerciseModel.ImportJobStaleAfter+time.Second)), "an interrupted import may run again")
}

func TestImportJobProgress(t *testing.T) {
	j := exerciseModel.NewImportJob(jobID, jobUser, jobSource, exerciseModel.ImportSourceUpload, uuid.NullUUID{}, jobT0)
	require.Equal(t, exerciseModel.ImportStageUnpacking, j.Stage(jobT0), "a new import is unpacking")

	p := exerciseModel.ImportProgress{Stage: exerciseModel.ImportStageImages, ImagesDone: 3, ImagesTotal: 7, TasksTotal: 12, BytesPushed: 300, BytesTotal: 700}
	require.True(t, j.Report(p, jobT0.Add(time.Minute)))
	require.Equal(t, p, j.Progress)
	require.Equal(t, jobT0.Add(time.Minute), j.UpdatedAt, "progress keeps a running import alive")
	require.Equal(t, exerciseModel.ImportStageImages, j.Stage(jobT0.Add(time.Minute)))

	j.Finish(make([]uuid.UUID, 12), jobT0.Add(2*time.Minute))
	require.Equal(t, exerciseModel.ImportStageDone, j.Stage(jobT0.Add(2*time.Minute)))
	require.Equal(t, 7, j.Progress.ImagesDone)
	require.Equal(t, 12, j.Progress.TasksDone)
	require.Equal(t, int64(700), j.Progress.BytesPushed)
	require.False(t, j.Report(p, jobT0.Add(3*time.Minute)), "a finished import keeps its outcome")
}

func TestImportJobProgressFailedAndRestart(t *testing.T) {
	j := exerciseModel.NewImportJob(jobID, jobUser, jobSource, exerciseModel.ImportSourceUpload, uuid.NullUUID{}, jobT0)
	j.Report(exerciseModel.ImportProgress{Stage: exerciseModel.ImportStageImages, ImagesDone: 1, ImagesTotal: 2}, jobT0)
	j.Fail(exerciseModel.ImportJobError{Code: 1, Message: "x"}, jobT0.Add(time.Minute))
	require.Equal(t, exerciseModel.ImportStageFailed, j.Stage(jobT0.Add(time.Minute)))
	require.Equal(t, 1, j.Progress.ImagesDone, "a failed import keeps how far it came")

	require.True(t, j.Restart(uuid.NullUUID{}, jobT0.Add(2*time.Minute)))
	require.Equal(t, exerciseModel.ImportProgress{Stage: exerciseModel.ImportStageUnpacking}, j.Progress)

	// An interrupted import reads as failed whatever stage it was left in.
	j.Report(exerciseModel.ImportProgress{Stage: exerciseModel.ImportStageTasks}, jobT0.Add(3*time.Minute))
	require.Equal(t, exerciseModel.ImportStageFailed, j.Stage(jobT0.Add(3*time.Minute).Add(exerciseModel.ImportJobStaleAfter+time.Second)))
}
