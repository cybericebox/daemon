package jobsModel

import (
	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"
)

// ImageBlobPushArgs streams one chunked upload (purpose image-blob) into the platform registry as the blob Digest.
// Unique per digest while queued or running: one push of a blob at a time.
type ImageBlobPushArgs struct {
	Digest   string    `json:"digest" river:"unique"`
	UploadID uuid.UUID `json:"upload_id"`
	Size     int64     `json:"size"`
}

func (ImageBlobPushArgs) Kind() string { return "image_blob_push" }

// InsertOpts: a few attempts at most. The push records its own failures; only a failure to write that state is
// returned to River, and a retry of a multi-GiB push should not run for long.
// It runs on ImagesQueue.
func (ImageBlobPushArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, Queue: ImagesQueue}
}

// ImagesQueue runs the long image work (blob pushes, archive imports with images) with a few workers of its own,
// so it never holds the default queue.
const ImagesQueue = "images"

// ImagesQueueWorkers is how many image jobs run at once on one replica.
const ImagesQueueWorkers = 2
