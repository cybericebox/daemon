package imageRegistryUseCase

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/provider"
)

const (
	// unusedTagGrace: a tag is kept this long after its upload, so an image uploaded for a draft that is not
	// saved yet is not collected under the author's hands.
	unusedTagGrace = 24 * time.Hour
	// stalePushAfter: a blob push state nobody came back to after a week is dropped.
	stalePushAfter = 7 * 24 * time.Hour
)

// CollectUnusedTags removes the platform registry tags no exercise version (draft, published, any status)
// references and that were uploaded more than a day ago, then drops their records; the registry collects the
// unused layers itself. It also drops the blob push states older than a week. It returns how many tags it
// removed. Not configured (no settings, no cipher): nothing to do. A provider failure keeps that row for the next
// pass and does not stop the others; the tags of one digest are removed together, and a package version that
// also carries tags of other rows waits until those go too.
func (u *ImageRegistryUseCase) CollectUnusedTags(ctx context.Context, now time.Time) (int, error) {
	if n, err := u.blobPushes.DeleteStale(ctx, now.Add(-stalePushAfter)); err != nil {
		log.Warn().Err(err).Msg("image registry: dropping stale blob push states failed")
	} else if n > 0 {
		log.Debug().Int64("count", n).Msg("image registry: dropped stale blob push states")
	}

	s, token, err := u.platformToken(ctx, true)
	if err != nil {
		return 0, err
	}
	if token == "" {
		return 0, nil
	}
	adapter := u.adapter(s.Provider)
	if adapter == nil {
		return 0, nil
	}
	cutoff := now.Add(-unusedTagGrace)
	rows, err := u.images.ListUnreferenced(ctx, cutoff)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to list the unused registry tags").Err()
	}

	// The tags of one digest go together: on GitHub they sit on one package version, which is deleted whole and
	// only when every tag it carries is being removed (a tag-by-tag removal would wait on the others forever).
	var digests []string
	tags := map[string][]string{}
	for _, row := range rows {
		if _, ok := tags[row.Digest]; !ok {
			digests = append(digests, row.Digest)
		}
		tags[row.Digest] = append(tags[row.Digest], row.Tag)
	}
	deleted := 0
	var failed error
	for _, digest := range digests {
		if err = ctx.Err(); err != nil {
			return deleted, err
		}
		// The list may be old by the time a digest comes up (a draft saved, the image uploaded again): ask again
		// right before its tags go, and skip it when it is no longer collectable.
		//
		// A small race stays: a version may start referencing the digest, or the image be uploaded again,
		// between this check and the tag removal. The conditional row delete keeps a re-uploaded row, and the
		// tag comes back with the next upload of the image (every upload pushes its manifest and tag again);
		// until then the image shows as not found.
		ok, cerr := u.images.IsCollectable(ctx, digest, cutoff)
		if cerr != nil {
			failed = errors.Join(failed, cerr)
		}
		if !ok {
			continue
		}
		// A row can exist whose tag was never pushed (the record precedes the manifest push): an absent tag is gone.
		derr := adapter.DeleteTags(ctx, s.Account, token, s.Repository, tags[digest])
		switch {
		case derr == nil, errors.Is(derr, ociregistry.ErrNotFound):
		case errors.Is(derr, provider.ErrTagShared):
			log.Debug().Str("digest", digest).Msg("image registry: tags share a package version with other tags, kept for the next pass")
			continue
		default:
			log.Warn().Err(derr).Str("digest", digest).Msg("image registry: removing unused tags failed, kept for the next pass")
			continue
		}
		for _, tag := range tags[digest] {
			removed, derr := u.images.Delete(ctx, digest, tag, cutoff)
			if derr != nil {
				failed = errors.Join(failed, derr)
				continue
			}
			if removed {
				deleted++
			}
		}
	}
	if failed != nil {
		return deleted, model.ErrPlatform.WithError(failed).WithMessage("Failed to drop the records of removed registry tags").Err()
	}
	return deleted, nil
}
