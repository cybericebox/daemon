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

// One import job per source: starting again returns the stored job; the whole job (state, created tasks, error)
// round-trips.
func TestExerciseImportJobRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	owner := newUser(t, db)
	source := uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000d1")
	first := exerciseModel.NewImportJob(uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000d2"), owner, source, exerciseModel.ImportSourceUpload, uuid.NullUUID{}, t0)

	stored, err := repo.StartImportJob(ctx, first)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if stored.ID != first.ID || stored.State != exerciseModel.ImportJobRunning || stored.Source != exerciseModel.ImportSourceUpload || len(stored.ExerciseIDs) != 0 || stored.Error != nil {
		t.Fatalf("stored %+v", stored)
	}
	again, err := repo.StartImportJob(ctx, exerciseModel.NewImportJob(uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000d3"), owner, source, exerciseModel.ImportSourceUpload, uuid.NullUUID{}, t0.Add(time.Minute)))
	if err != nil || again.ID != first.ID || !again.CreatedAt.Equal(t0) {
		t.Fatalf("again %+v err %v", again, err)
	}

	progress := exerciseModel.ImportProgress{Stage: exerciseModel.ImportStageImages, ImagesDone: 1, ImagesTotal: 2, TasksTotal: 3, BytesPushed: 10, BytesTotal: 25}
	stored.Report(progress, t0.Add(90*time.Second))
	if n, saveErr := repo.SaveImportJobProgress(ctx, stored); saveErr != nil || n != 1 {
		t.Fatalf("progress: %d %v", n, saveErr)
	}
	if got, getErr := repo.GetImportJob(ctx, first.ID); getErr != nil || got.Progress != progress || got.State != exerciseModel.ImportJobRunning || !got.UpdatedAt.Equal(t0.Add(90*time.Second)) {
		t.Fatalf("progress job %+v err %v", got, getErr)
	}

	stored.Fail(exerciseModel.ImportJobError{Code: 70993, Message: "m", Context: map[string]any{"file": "images/web.tar"}}, t0.Add(2*time.Minute))
	if n, saveErr := repo.SaveImportJob(ctx, stored); saveErr != nil || n != 1 {
		t.Fatalf("save failed: %d %v", n, saveErr)
	}
	got, err := repo.GetImportJob(ctx, first.ID)
	if err != nil || got.State != exerciseModel.ImportJobFailed || got.Error == nil || got.Error.Code != 70993 || got.Error.Context["file"] != "images/web.tar" || !got.UpdatedAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("failed job %+v err %v", got, err)
	}

	// A progress write never changes an ended job.
	late := got
	late.Progress.ImagesDone = 2
	if n, progressErr := repo.SaveImportJobProgress(ctx, late); progressErr != nil || n != 0 {
		t.Fatalf("progress of a failed job: %d %v", n, progressErr)
	}
	if again, getErr := repo.GetImportJob(ctx, first.ID); getErr != nil || again.Progress.ImagesDone != 1 {
		t.Fatalf("failed job changed: %+v %v", again, getErr)
	}

	created := []uuid.UUID{uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000e1")}
	got.Finish(created, t0.Add(3*time.Minute))
	if _, err = repo.SaveImportJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetImportJob(ctx, first.ID)
	if err != nil || got.State != exerciseModel.ImportJobDone || got.Error != nil || len(got.ExerciseIDs) != 1 || got.ExerciseIDs[0] != created[0] {
		t.Fatalf("done job %+v err %v", got, err)
	}

	if _, err = repo.GetImportJob(ctx, uuid.FromStringOrNil("01900000-0000-7000-8000-0000000000ff")); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent: %v", err)
	}
}

// The target event of a moderator's import round-trips, a restart may move it, and the event going away removes
// the job (nothing is created in the catalog instead).
func TestExerciseImportJobOwnerEvent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	t0 := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	user := newUser(t, db)
	eventA, eventB := mustSeedEventForParticipants(t, db, "importa"), mustSeedEventForParticipants(t, db, "importb")
	target := uuid.NullUUID{UUID: eventA.ID, Valid: true}
	job := exerciseModel.NewImportJob(uuid.Must(uuid.NewV7()), user, uuid.Must(uuid.NewV7()), exerciseModel.ImportSourceUpload, target, t0)
	stored, err := repo.StartImportJob(ctx, job)
	if err != nil || stored.OwnerEventID != target {
		t.Fatalf("stored %+v err %v", stored, err)
	}
	stored.Fail(exerciseModel.ImportJobError{Code: 1, Message: "m"}, t0.Add(time.Minute))
	moved := uuid.NullUUID{UUID: eventB.ID, Valid: true}
	if !stored.Restart(moved, t0.Add(2*time.Minute)) {
		t.Fatal("a failed import restarts")
	}
	if _, err = repo.SaveImportJob(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if got, getErr := repo.GetImportJob(ctx, job.ID); getErr != nil || got.OwnerEventID != moved {
		t.Fatalf("restarted into %+v err %v", got.OwnerEventID, getErr)
	}
	if _, err = db.Pool.Exec(ctx, `DELETE FROM events WHERE id = $1`, eventB.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetImportJob(ctx, job.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("the event's import goes with it: %v", err)
	}
}
