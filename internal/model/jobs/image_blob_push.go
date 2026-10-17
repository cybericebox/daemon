package jobsModel

import "github.com/gofrs/uuid"

// ImageBlobPushArgs streams one chunked upload (purpose image-blob) into the platform registry as the blob Digest.
// Unique per digest while queued or running: one push of a blob at a time.
type ImageBlobPushArgs struct {
	Digest   string    `json:"digest" river:"unique"`
	UploadID uuid.UUID `json:"upload_id"`
	Size     int64     `json:"size"`
}

func (ImageBlobPushArgs) Kind() string { return "image_blob_push" }
