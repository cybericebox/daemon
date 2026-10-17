package imageRegistryRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

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

// ErrTagTaken: Save hit UNIQUE (tag): a row of another digest already maps the tag. The use case maps it to
// imageRegistryModel.ErrImageTagTaken (one call site for that code).
var ErrTagTaken = errors.New("platform image tag taken by another digest")

const tagUniqueConstraint = "platform_images_tag_key"

// Save records an upload in one statement; the same digest and tag again refreshes the row (upload time included).
// The same tag for another digest fails with ErrTagTaken.
func (r *PlatformImages) Save(ctx context.Context, img imageRegistryModel.PlatformImage) error {
	err := r.q.UpsertPlatformImage(ctx, postgres.UpsertPlatformImageParams{
		Digest:     img.Digest,
		Tag:        img.Tag,
		Name:       img.Name,
		ExerciseID: nullable(img.ExerciseID),
		UploadedBy: nullable(img.UploadedBy),
		UploadedAt: img.UploadedAt,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == tagUniqueConstraint {
		return errors.Join(ErrTagTaken, err)
	}
	return err
}

// TagTakenByOtherDigest reports whether a row of a digest other than this one already maps the tag.
func (r *PlatformImages) TagTakenByOtherDigest(ctx context.Context, tag, digest string) (bool, error) {
	return r.q.PlatformImageTagTakenByOtherDigest(ctx, postgres.PlatformImageTagTakenByOtherDigestParams{Tag: tag, Digest: digest})
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

// IsCollectable asks again whether the digest may still be removed: no exercise version references it, none of
// its rows was uploaded at/after the cutoff, and none of its tags is also mapped by another digest.
func (r *PlatformImages) IsCollectable(ctx context.Context, digest string, before time.Time) (bool, error) {
	v, err := r.q.IsDigestCollectable(ctx, postgres.IsDigestCollectableParams{TargetDigest: digest, Before: before})
	return v.Valid && v.Bool, err
}

// Delete removes the record of one tag (it is gone from the registry), only while its digest is still
// collectable; removed is false when the row was kept (or already gone).
func (r *PlatformImages) Delete(ctx context.Context, digest, tag string, before time.Time) (removed bool, err error) {
	n, err := r.q.DeletePlatformImage(ctx, postgres.DeletePlatformImageParams{Digest: digest, Tag: tag, Before: before})
	return n > 0, err
}
