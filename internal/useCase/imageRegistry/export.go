package imageRegistryUseCase

import (
	"context"
	"errors"
	"io"
	"strings"

	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// Export of platform images into task archives («з образами»): a platform reference is read from the platform
// repository with the pull credential (the push token when no pull token is set) and written as an image archive
// (ociregistry.ImageLayout: OCI layout + docker load manifest.json), its blobs streamed from the registry.

// maxExportManifestBytes caps a manifest read for an export.
const maxExportManifestBytes = 4 << 20

// ImageExport is one platform image ready to be written as an image archive.
type ImageExport struct {
	layout ociregistry.ImageLayout
	client *ociregistry.Client
}

// Digest is the image's manifest digest.
func (e ImageExport) Digest() string { return e.layout.ManifestDigest }

// Size is the exact length of the archive WriteTo writes.
func (e ImageExport) Size() int64 { return e.layout.Size() }

// WriteTo streams the image archive into w. A registry failure (a blob gone, the token refused, wrong bytes) is
// ErrImageNotExportable with its reason; a failure of w and a cancellation are returned as they are.
func (e ImageExport) WriteTo(ctx context.Context, w io.Writer) error {
	err := e.layout.WriteTo(w, func(digest string) (io.ReadCloser, error) {
		rc, _, openErr := e.client.OpenBlob(ctx, digest)
		return rc, openErr
	})
	if err != nil && registryFailure(err) && ctx.Err() == nil {
		return imageNotExportable(ReasonOf(err), err)
	}
	return err
}

// ResolveImageExport reads the manifest of a platform reference and returns the image ready to write. Refusals are
// ErrImageNotExportable: the registry not configured (not_configured), not a platform reference
// (invalid_reference), not one image (invalid_image), or the registry's failure (its reason).
func (u *ImageRegistryUseCase) ResolveImageExport(ctx context.Context, reference string) (ImageExport, error) {
	ref, ok := imageRegistryModel.ParsePlatformRef(reference)
	if !ok {
		return ImageExport{}, imageNotExportable(imageRegistryModel.ReasonInvalidReference, nil)
	}
	client, _, err := u.PlatformRegistryClient(ctx, false)
	if err != nil {
		if errors.Is(err, imageRegistryModel.ErrRegistryNotConfigured.Err()) {
			return ImageExport{}, imageNotExportable(imageRegistryModel.ReasonNotConfigured, err)
		}
		return ImageExport{}, err
	}
	mediaType, body, err := client.GetManifest(ctx, ref.Digest, maxExportManifestBytes)
	switch {
	case errors.Is(err, ociregistry.ErrBlobTooLarge):
		return ImageExport{}, imageNotExportable(imageRegistryModel.ReasonInvalidImage, err)
	case err != nil && ctx.Err() != nil:
		return ImageExport{}, err
	case err != nil:
		return ImageExport{}, imageNotExportable(ReasonOf(err), err)
	}
	layout, err := ociregistry.ParseImageLayout(mediaType, body, ref.Name+":"+strings.TrimPrefix(ref.Digest, "sha256:")[:12])
	if err != nil {
		return ImageExport{}, imageNotExportable(imageRegistryModel.ReasonInvalidImage, err)
	}
	return ImageExport{layout: layout, client: client}, nil
}

// registryFailure tells a registry client failure from anything else (the destination's own error).
func registryFailure(err error) bool {
	for _, sentinel := range []error{ociregistry.ErrUnauthorized, ociregistry.ErrNotFound, ociregistry.ErrUnreachable, ociregistry.ErrRateLimited, ociregistry.ErrInvalidReference} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// imageNotExportable is the one place ErrImageNotExportable is raised; cause stays in the server log.
func imageNotExportable(reason imageRegistryModel.Reason, cause error) error {
	e := imageRegistryModel.ErrImageNotExportable.WithPublicContext(imageRegistryModel.ContextReason, string(reason))
	if cause != nil {
		e = e.WithError(cause)
	}
	return e.Err()
}
