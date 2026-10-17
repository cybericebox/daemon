package imageRegistryRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// PlatformImages records the images pushed to the platform registry (one row per digest and tag) for the tag
// collector.
type PlatformImages struct {
	q Queries
}

func NewPlatformImages(q Queries) *PlatformImages {
	return &PlatformImages{q: q}
}

// Save records an upload in one statement; the same digest and tag again refreshes the row (upload time included).
func (r *PlatformImages) Save(ctx context.Context, img imageRegistryModel.PlatformImage) error {
	return r.q.UpsertPlatformImage(ctx, postgres.UpsertPlatformImageParams{
		Digest:     img.Digest,
		Tag:        img.Tag,
		Name:       img.Name,
		ExerciseID: nullable(img.ExerciseID),
		UploadedBy: nullable(img.UploadedBy),
		UploadedAt: img.UploadedAt,
	})
}

func nullable(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

// ListUnreferenced returns every row of the digests no exercise version references and whose rows were all uploaded
// before the cutoff: the tags the collector may remove.
func (r *PlatformImages) ListUnreferenced(ctx context.Context, before time.Time) ([]imageRegistryModel.PlatformImage, error) {
	rows, err := r.q.ListUnreferencedPlatformImages(ctx, before)
	if err != nil {
		return nil, err
	}
	out := make([]imageRegistryModel.PlatformImage, 0, len(rows))
	for _, row := range rows {
		out = append(out, imageRegistryModel.PlatformImage{
			Digest: row.Digest, Tag: row.Tag, Name: row.Name,
			ExerciseID: row.ExerciseID.UUID, UploadedBy: row.UploadedBy.UUID, UploadedAt: row.UploadedAt,
		})
	}
	return out, nil
}

// Delete removes the record of one tag (it is gone from the registry).
func (r *PlatformImages) Delete(ctx context.Context, digest, tag string) error {
	return r.q.DeletePlatformImage(ctx, postgres.DeletePlatformImageParams{Digest: digest, Tag: tag})
}
