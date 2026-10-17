package imageRegistryRepo

import (
	"context"

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
