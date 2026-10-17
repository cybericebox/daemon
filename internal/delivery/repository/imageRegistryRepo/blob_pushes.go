package imageRegistryRepo

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// BlobPushes is the state of the background image blob pushes (one row per digest, removed on success).
type BlobPushes struct {
	q Queries
}

func NewBlobPushes(q Queries) *BlobPushes {
	return &BlobPushes{q: q}
}

// Save writes the whole push state in one statement.
func (r *BlobPushes) Save(ctx context.Context, p imageRegistryModel.BlobPush) error {
	return r.q.UpsertImageBlobPush(ctx, postgres.UpsertImageBlobPushParams{
		Digest: p.Digest, State: string(p.State), Reason: string(p.Reason), UpdatedAt: p.UpdatedAt,
	})
}

// Get loads the push state of a digest; found is false when none is recorded.
func (r *BlobPushes) Get(ctx context.Context, digest string) (imageRegistryModel.BlobPush, bool, error) {
	row, err := r.q.GetImageBlobPush(ctx, digest)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return imageRegistryModel.BlobPush{}, false, nil
		}
		return imageRegistryModel.BlobPush{}, false, err
	}
	return imageRegistryModel.BlobPush{
		Digest: row.Digest, State: imageRegistryModel.BlobState(row.State), Reason: imageRegistryModel.Reason(row.Reason), UpdatedAt: row.UpdatedAt,
	}, true, nil
}

// Delete removes the push state of a digest (the push succeeded).
func (r *BlobPushes) Delete(ctx context.Context, digest string) error {
	return r.q.DeleteImageBlobPush(ctx, digest)
}

// DeleteStale removes the push states not touched since the cutoff and returns how many went.
func (r *BlobPushes) DeleteStale(ctx context.Context, before time.Time) (int64, error) {
	return r.q.DeleteStaleImageBlobPushes(ctx, before)
}
