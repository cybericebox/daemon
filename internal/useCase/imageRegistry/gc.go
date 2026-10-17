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
// pass and does not stop the others; a tag shared with other tags of its package version waits until they go too.
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
	rows, err := u.images.ListUnreferenced(ctx, now.Add(-unusedTagGrace))
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to list the unused registry tags").Err()
	}

	deleted := 0
	var failed error
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return deleted, err
		}
		// A row can exist whose tag was never pushed (the record precedes the manifest push): an absent tag is gone.
		derr := adapter.DeleteTag(ctx, s.Account, token, s.Repository, row.Tag)
		switch {
		case derr == nil, errors.Is(derr, ociregistry.ErrNotFound):
		case errors.Is(derr, provider.ErrTagShared):
			log.Debug().Str("tag", row.Tag).Msg("image registry: tag shares its package version, kept for the next pass")
			continue
		default:
			log.Warn().Err(derr).Str("tag", row.Tag).Msg("image registry: removing an unused tag failed, kept for the next pass")
			continue
		}
		if derr = u.images.Delete(ctx, row.Digest, row.Tag); derr != nil {
			failed = errors.Join(failed, derr)
			continue
		}
		deleted++
	}
	if failed != nil {
		return deleted, model.ErrPlatform.WithError(failed).WithMessage("Failed to drop the records of removed registry tags").Err()
	}
	return deleted, nil
}
