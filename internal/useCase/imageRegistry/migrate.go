package imageRegistryUseCase

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// ErrMigrateNeedsOwn: `images migrate-uploads` copies into the own registry, so the deployment must configure it.
var ErrMigrateNeedsOwn = errors.New("the own platform registry is not configured (PLATFORM_REGISTRY_HOST, _USERNAME, _PASSWORD)")

// ErrMigrateNoSource: there is no Docker Hub / GHCR registry saved in the platform panel to copy from.
var ErrMigrateNoSource = errors.New("no provider registry (Docker Hub, GHCR) is saved in the platform settings: nothing to copy from")

// MigrateReport counts what MigrateUploads did.
type MigrateReport struct {
	// Images is how many (name, digest) pairs were looked at.
	Images int
	// Copied images were not in the own registry and now are; Present ones already were.
	Copied, Present int
	// Failed images could not be copied (the source no longer has them, the registry refused); see the lines
	// passed to the progress function.
	Failed int
}

// MigrateUploads copies the images uploaded while a provider registry (Docker Hub, GHCR) was the platform
// registry into the own registry, under the repository platform/<name> of each platform:<name>@<digest> that is
// recorded (platform_images) or that a device of any exercise version names. The references of the exercises do
// not change: a reference is the name and the digest, and both stay. An image the own registry already has is
// skipped, so the command is safe to run again and finishes what an interrupted run left. Nothing is deleted from
// the source registry or from the database; the settings of the panel are left as they are (the own registry is
// switched on by the deployment's variables, and the provider settings may be cleared afterwards).
//
// Blobs stream from the source to the own registry through a digest check, the manifest is pushed under the
// platform tag and its digest must be the one asked for. One failed image does not stop the others; each
// outcome goes to progress as one line without any credential.
func (u *ImageRegistryUseCase) MigrateUploads(ctx context.Context, progress func(string)) (MigrateReport, error) {
	var report MigrateReport
	if !u.own.Enabled() {
		return report, ErrMigrateNeedsOwn
	}
	s, token, err := u.platformToken(ctx, false)
	if err != nil {
		return report, err
	}
	if token == "" {
		return report, ErrMigrateNoSource
	}
	source := u.newClient(s.Provider, s.Repository, s.Account, token)

	targets, err := u.migrationTargets(ctx)
	if err != nil {
		return report, err
	}
	report.Images = len(targets)
	for _, ref := range targets {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		copied, cerr := u.migrateOne(ctx, source, ref)
		switch {
		case cerr != nil:
			report.Failed++
			progress(fmt.Sprintf("FAILED %s: %s", ref.String(), ReasonOf(cerr)))
		case copied:
			report.Copied++
			progress("copied " + ref.String())
		default:
			report.Present++
			progress("present " + ref.String())
		}
	}
	return report, nil
}

// migrationTargets lists every platform reference to copy, once each, in a stable order: the recorded images by
// their recorded name, and every reference a device names (the name there may differ from the recorded one).
func (u *ImageRegistryUseCase) migrationTargets(ctx context.Context) ([]imageRegistryModel.PlatformRef, error) {
	rows, err := u.images.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the recorded platform images: %w", err)
	}
	referenced, err := u.images.ReferencedImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the referenced platform images: %w", err)
	}
	seen := map[string]bool{}
	var out []imageRegistryModel.PlatformRef
	add := func(ref imageRegistryModel.PlatformRef) {
		if !seen[ref.String()] {
			seen[ref.String()] = true
			out = append(out, ref)
		}
	}
	for _, row := range rows {
		if ref, rerr := imageRegistryModel.NewPlatformRef(row.Name, row.Digest); rerr == nil {
			add(ref)
		}
	}
	for _, image := range referenced {
		if ref, ok := imageRegistryModel.ParsePlatformRef(image); ok {
			add(ref)
		}
	}
	slices.SortFunc(out, func(a, b imageRegistryModel.PlatformRef) int {
		return strings.Compare(a.String(), b.String())
	})
	return out, nil
}

// migrateOne copies one image; copied is false when the own registry already had it.
func (u *ImageRegistryUseCase) migrateOne(ctx context.Context, source *ociregistry.Client, ref imageRegistryModel.PlatformRef) (copied bool, err error) {
	target := u.ownClient(ref.Repository())
	have, err := target.ManifestExists(ctx, ref.Digest)
	if err != nil {
		return false, err
	}
	if have {
		return false, nil
	}
	mediaType, body, err := source.GetManifest(ctx, ref.Digest, maxExportManifestBytes)
	if err != nil {
		return false, err
	}
	layout, err := ociregistry.ParseImageLayout(mediaType, body, "")
	if err != nil {
		return false, fmt.Errorf("%w: %v", ociregistry.ErrInvalidReference, err)
	}
	blobs := append([]ociregistry.ExportBlob{layout.Config}, layout.Layers...)
	seen := map[string]bool{}
	for _, blob := range blobs {
		if seen[blob.Digest] {
			continue
		}
		seen[blob.Digest] = true
		if err = copyBlob(ctx, source, target, blob); err != nil {
			return false, err
		}
	}
	got, err := target.PutManifest(ctx, ref.Tag(), layout.ManifestType, layout.Manifest)
	if err != nil {
		return false, err
	}
	if got != ref.Digest {
		return false, fmt.Errorf("%w: the manifest is not the one asked for", ociregistry.ErrInvalidReference)
	}
	return true, nil
}

// copyBlob streams one blob from the source into the target repository unless it is there, checking its bytes
// against the digest on the way (a wrong blob never completes in the registry).
func copyBlob(ctx context.Context, source, target *ociregistry.Client, blob ociregistry.ExportBlob) error {
	have, err := target.BlobExists(ctx, blob.Digest)
	if err != nil {
		return err
	}
	if have {
		return nil
	}
	rc, size, err := source.OpenBlob(ctx, blob.Digest)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	vr := &verifyingReader{r: rc, h: sha256.New(), want: blob.Digest}
	if err = target.PushBlob(ctx, blob.Digest, size, vr); err != nil {
		if vr.mismatch {
			return fmt.Errorf("%w: blob bytes do not match the digest", ociregistry.ErrInvalidReference)
		}
		return err
	}
	return nil
}
