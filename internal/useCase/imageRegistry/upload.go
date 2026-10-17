package imageRegistryUseCase

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Image upload through the site. The browser reads the blob digests of a `docker save` archive, asks which ones
// the platform registry lacks (MissingBlobs), sends each missing blob through the chunked media upload and has it
// pushed (PushStagedBlob), then creates the image (CreateImage): the manifest is built, pushed under the platform
// tag and recorded. The exercise edit access is checked by the caller (the route). The registry not configured:
// ErrRegistryNotConfigured; the registry failing: ErrRegistryUnavailable with the reason for admins. Nothing is
// recorded unless the manifest push succeeded.

type (
	// MediaSource reads the files staged through the chunked media upload.
	MediaSource interface {
		GetFile(ctx context.Context, id uuid.UUID) (mediaModel.File, error)
		StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error)
	}

	// Descriptor names one blob of an image.
	Descriptor = imageRegistryModel.Descriptor

	// PushBlobInput names a staged media file and the blob it must be.
	PushBlobInput struct {
		FileID uuid.UUID
		Digest string
		Size   int64
	}

	// CreateImageInput is an image made of blobs already in the registry.
	CreateImageInput struct {
		// Name is sanitized into the platform image name.
		Name   string
		Config Descriptor
		Layers []Descriptor
	}

	CreateImageResult struct {
		// Reference is platform:<name>@sha256:<manifest digest>, what the device stores.
		Reference string
	}
)

// pushTokenName names the push token in the refusal's public context.
const pushTokenName = "push"

// MissingBlobs returns the digests (deduplicated, in the order sent) the platform registry does not have.
func (u *ImageRegistryUseCase) MissingBlobs(ctx context.Context, _ uuid.UUID, digests []string) ([]string, error) {
	if err := checkDigests(digests...); err != nil {
		return nil, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0, len(digests))
	seen := make(map[string]bool, len(digests))
	for _, d := range digests {
		if seen[d] {
			continue
		}
		seen[d] = true
		ok, err := client.BlobExists(ctx, d)
		if err != nil {
			return nil, Unavailable(err, pushTokenName)
		}
		if !ok {
			missing = append(missing, d)
		}
	}
	return missing, nil
}

// PushStagedBlob streams a staged media file of the caller into the platform registry as the named blob. The
// file's sha256 (computed when its upload completed) and size must be the blob's; a blob the registry already has
// is not sent again. The staged file is left to the media garbage collector: it is referenced by nothing, so the
// next collection pass after the grace period removes it.
func (u *ImageRegistryUseCase) PushStagedBlob(ctx context.Context, _ uuid.UUID, in PushBlobInput) error {
	if err := checkDigests(in.Digest); err != nil {
		return err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return err
	}
	if u.media == nil {
		return model.ErrPlatform.WithMessage("Staged image blobs cannot be read").Err()
	}
	claims, signedIn := rbac.CurrentUserSessionFromContext(ctx)
	file, err := u.media.GetFile(ctx, in.FileID)
	foreign := err == nil && (!signedIn || !file.CreatedBy.Valid || file.CreatedBy.UUID != claims.UserID)
	if errors.Is(err, mediaModel.ErrFileNotFound.Err()) || foreign {
		return imageRegistryModel.ErrStagedBlobNotFound.WithError(err).Err()
	}
	if err != nil {
		return err
	}
	if "sha256:"+strings.ToLower(file.ContentHash) != in.Digest || file.SizeBytes != in.Size {
		return imageRegistryModel.ErrBlobDigestMismatch.Err()
	}
	exists, err := client.BlobExists(ctx, in.Digest)
	if err != nil {
		return Unavailable(err, pushTokenName)
	}
	if exists {
		return nil
	}
	rc, _, err := u.media.StreamFile(ctx, in.FileID)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	if err = client.PushBlob(ctx, in.Digest, in.Size, rc); err != nil {
		return Unavailable(err, pushTokenName)
	}
	return nil
}

// CreateImage builds the image manifest from blobs the registry already has, pushes it under the platform tag
// (PlatformRef.Tag) and records the image. Every blob must be in the registry (ErrBlobMissing names the first one
// that is not); config and layers together are at most MaxImageArchiveBytes.
func (u *ImageRegistryUseCase) CreateImage(ctx context.Context, exerciseID uuid.UUID, in CreateImageInput) (CreateImageResult, error) {
	manifest, err := imageRegistryModel.BuildManifest(in.Config, in.Layers)
	if err != nil {
		return CreateImageResult{}, err
	}
	ref, err := imageRegistryModel.NewPlatformRef(imageRegistryModel.SanitizeName(in.Name), manifest.Digest)
	if err != nil {
		return CreateImageResult{}, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return CreateImageResult{}, err
	}
	seen := map[string]bool{}
	for _, d := range append([]Descriptor{in.Config}, in.Layers...) {
		if seen[d.Digest] {
			continue
		}
		seen[d.Digest] = true
		ok, err := client.BlobExists(ctx, d.Digest)
		if err != nil {
			return CreateImageResult{}, Unavailable(err, pushTokenName)
		}
		if !ok {
			return CreateImageResult{}, imageRegistryModel.ErrBlobMissing.WithPublicContext(imageRegistryModel.ContextDigest, d.Digest).Err()
		}
	}
	if _, err = client.PutManifest(ctx, ref.Tag(), manifest.MediaType, manifest.Body); err != nil {
		return CreateImageResult{}, Unavailable(err, pushTokenName)
	}
	var uploadedBy uuid.UUID
	if claims, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		uploadedBy = claims.UserID
	}
	if err = u.images.Save(ctx, imageRegistryModel.NewPlatformImage(ref, exerciseID, uploadedBy, u.now())); err != nil {
		return CreateImageResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record the uploaded image").Err()
	}
	return CreateImageResult{Reference: ref.String()}, nil
}

// checkDigests refuses any digest that is not sha256 with 64 lowercase hex characters.
func checkDigests(digests ...string) error {
	for _, d := range digests {
		if !imageRegistryModel.ValidDigest(d) {
			return imageRegistryModel.ErrInvalidBlobDigest.Err()
		}
	}
	return nil
}
