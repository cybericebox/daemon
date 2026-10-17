package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The whole export job (tasks, file name, state, stored file, error) round-trips.
func TestExerciseExportJobRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	owner := newUser(t, db)
	tasks := []uuid.UUID{uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a1"), uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a2")}
	job := exerciseModel.NewExportJob(uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a3"), owner, tasks, "exercises.cybericebox.zip", t0)

	if err := repo.CreateExportJob(ctx, job); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetExportJob(ctx, job.ID)
	if err != nil || got.State != exerciseModel.ImportJobRunning || got.CreatedBy != owner || len(got.ExerciseIDs) != 2 || got.ExerciseIDs[1] != tasks[1] ||
		got.FileName != "exercises.cybericebox.zip" || got.FileID != uuid.Nil || got.Error != nil || !got.CreatedAt.Equal(t0) {
		t.Fatalf("stored %+v err %v", got, err)
	}

	got.Fail(exerciseModel.ImportJobError{Code: 72830, Message: "m", Context: map[string]any{"device": "web", "reason": "not_found"}}, t0.Add(time.Minute))
	if n, saveErr := repo.SaveExportJob(ctx, got); saveErr != nil || n != 1 {
		t.Fatalf("save failed: %d %v", n, saveErr)
	}
	got, err = repo.GetExportJob(ctx, job.ID)
	if err != nil || got.State != exerciseModel.ImportJobFailed || got.Error == nil || got.Error.Context["device"] != "web" || !got.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("failed job %+v err %v", got, err)
	}

	file := uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000a4")
	got.Finish(file, t0.Add(2*time.Minute))
	if _, err = repo.SaveExportJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetExportJob(ctx, job.ID)
	if err != nil || got.State != exerciseModel.ImportJobDone || got.Error != nil || got.FileID != file {
		t.Fatalf("done job %+v err %v", got, err)
	}

	if _, err = repo.GetExportJob(ctx, uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000ff")); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent: %v", err)
	}
}
