package imageRegistryModel

import (
	"encoding/json"
	"time"
)

// MaxImageConfigBytes bounds the image config blob read to check the image (a real config is a few KiB).
const MaxImageConfigBytes = 8 << 20

// CheckImageConfig checks an image config blob: a linux/amd64 image whose rootfs lists one diff id per layer.
func CheckImageConfig(body []byte, layers int) error {
	var cfg struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		RootFS       struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if json.Unmarshal(body, &cfg) != nil || cfg.OS != "linux" || cfg.Architecture != "amd64" ||
		len(cfg.RootFS.DiffIDs) != layers || layers == 0 {
		return ErrInvalidImageConfig.Err()
	}
	return nil
}

// BlobState is where one blob of an image upload stands.
type BlobState string

const (
	// BlobPresent: the platform registry has the blob.
	BlobPresent BlobState = "present"
	// BlobPushing: a background push from the chunked upload is queued or running.
	BlobPushing BlobState = "pushing"
	// BlobFailed: the last push failed (Reason says why); push the upload again or upload it again.
	BlobFailed BlobState = "failed"
	// BlobMissing: the registry does not have it and no push is known.
	BlobMissing BlobState = "missing"
)

// BlobPushStaleAfter is how long a push may go without a word before it counts as failed (unreachable): a lost
// job never leaves a blob "pushing" forever. A 10 GiB push needs about 1 MB/s to finish in time.
const BlobPushStaleAfter = 3 * time.Hour

// BlobPush is the state of a background push of one blob (table image_blob_pushes, one row per digest).
// Success removes the row: the registry itself then answers "present".
type BlobPush struct {
	Digest    string
	State     BlobState
	Reason    Reason
	UpdatedAt time.Time
}

// NewBlobPush records a push queued (or starting) at now.
func NewBlobPush(digest string, now time.Time) BlobPush {
	return BlobPush{Digest: digest, State: BlobPushing, UpdatedAt: now}
}

// Fail marks the push failed for reason.
func (p *BlobPush) Fail(reason Reason, now time.Time) {
	p.State, p.Reason, p.UpdatedAt = BlobFailed, reason, now
}

// Current is the state as of now: a push silent for longer than BlobPushStaleAfter is failed (unreachable).
func (p *BlobPush) Current(now time.Time) (BlobState, Reason) {
	if p.State == BlobPushing && now.Sub(p.UpdatedAt) > BlobPushStaleAfter {
		return BlobFailed, ReasonUnreachable
	}
	return p.State, p.Reason
}
