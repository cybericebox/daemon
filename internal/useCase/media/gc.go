package media

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// CleanupOrphanFiles is the GC pass: drop grace-expired unreferenced files,
// then physically remove blobs nothing points at. S3 removal precedes the row
// delete; a failed removal leaves the row for the next pass (at-least-once).
//
// ListOrphanBlobs is also grace-gated on file_blobs.touched_at (same GCGrace
// cutoff): without it, a concurrent dedup upload of the exact content GC just
// listed as orphan (Stat sees the blob, skips the copy, CreateFile bumps
// ref_count) can still lose the race against the unconditional storage.Remove
// below — the row survives (DeleteBlob's ref_count <= 0 guard refuses it) but
// the live S3 object is already gone. Requiring the blob to have gone
// untouched for the whole grace window means a fresh upload's touch (which
// CreateFile stamps unconditionally, on both the insert and the dedup-hit
// conflict path) keeps it out of this pass's candidate list entirely.
func (u *MediaUseCase) CleanupOrphanFiles(ctx context.Context) error {
	if !u.storageConfigured {
		return nil // nothing uploaded ever — nothing to collect
	}
	cutoff := time.Now().Add(-u.cfg.GCGrace)
	if err := u.files.DeleteUnreferenced(ctx, cutoff); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete unreferenced files").Err()
	}
	orphans, err := u.files.ListOrphanBlobs(ctx, cutoff)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list orphan blobs").Err()
	}
	for _, hash := range orphans {
		if err = u.storage.Remove(ctx, mediaModel.BlobKey(hash)); err != nil {
			log.Warn().Err(err).Str("blob", hash).Msg("media gc: blob removal failed, will retry next pass")
			continue
		}
		if _, err = u.files.DeleteBlob(ctx, hash); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to delete blob row").Err()
		}
	}
	return nil
}
