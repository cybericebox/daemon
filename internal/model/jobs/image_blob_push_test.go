package jobsModel

import "testing"

// A blob push is retried a few times at most (only a failure to record its state is returned to River).
func TestImageBlobPushArgsInsertOpts(t *testing.T) {
	if got := (ImageBlobPushArgs{}).InsertOpts().MaxAttempts; got != 3 {
		t.Fatalf("max attempts %d", got)
	}
}

// Image pushes and archive imports run long: they get their own small queue, so they never hold the default one.
func TestImageJobsRunOnTheImagesQueue(t *testing.T) {
	if q := (ImageBlobPushArgs{}).InsertOpts().Queue; q != ImagesQueue {
		t.Fatalf("blob push queue %q", q)
	}
	if q := (ExerciseImportArgs{}).InsertOpts().Queue; q != ImagesQueue || ImagesQueue != "images" {
		t.Fatalf("import queue %q", q)
	}
}

// An export «з образами» pulls up to 10 GiB: the images queue too, a few attempts, a kind of its own.
func TestExerciseExportArgsRunOnTheImagesQueue(t *testing.T) {
	opts := (ExerciseExportArgs{}).InsertOpts()
	if opts.Queue != ImagesQueue || opts.MaxAttempts != 3 {
		t.Fatalf("export opts %+v", opts)
	}
	if kind := (ExerciseExportArgs{}).Kind(); kind != "exercise_export" {
		t.Fatalf("kind %q", kind)
	}
}
