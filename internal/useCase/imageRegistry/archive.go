package imageRegistryUseCase

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// Images inside a task archive (`image: {file: …}`). The import reads each image file of the archive twice, straight
// from the zip, never spooled: IndexImageArchive (pass 1) finds the image like the browser reader does, and
// PushImageArchive (pass 2) streams the blobs the registry lacks into it, hashing them on the way, then publishes
// the image through the same core as the browser upload (publishImage).

// PushArchiveInput is one indexed image file to publish.
type PushArchiveInput struct {
	// ExerciseID is recorded with the image; uuid.Nil while the importing task does not exist yet.
	ExerciseID uuid.UUID
	// UploadedBy is the user who started the import.
	UploadedBy uuid.UUID
	// Name is sanitized into the platform image name (the device name).
	Name    string
	Archive *ociregistry.ImageArchive
}

// ArchiveImageInspection is a read-only observation, never a reservation.
type ArchiveImageInspection struct {
	Reference string
	Digest    string
	Reused    bool
}

// InspectImageArchive shares the push manifest/ref construction. Indexing has
// already read and verified every blob. Only the digest tag is requested here;
// no registry bytes, ownership claims or platform_images rows are written.
func (u *ImageRegistryUseCase) InspectImageArchive(ctx context.Context, archive *ociregistry.ImageArchive, name string) (ArchiveImageInspection, error) {
	layers := make([]Descriptor, 0, len(archive.Layers))
	for _, layer := range archive.Layers {
		layers = append(layers, descriptorOf(layer))
	}
	manifest, err := imageRegistryModel.BuildManifest(descriptorOf(archive.Config), layers)
	if err != nil {
		return ArchiveImageInspection{}, err
	}
	ref, err := imageRegistryModel.NewPlatformRef(imageRegistryModel.SanitizeName(name), manifest.Digest)
	if err != nil {
		return ArchiveImageInspection{}, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, false)
	if err != nil {
		return ArchiveImageInspection{}, err
	}
	have, found, err := client.ManifestDigest(ctx, ref.Tag())
	if err != nil {
		return ArchiveImageInspection{}, Unavailable(err, "")
	}
	if found && have != "" && have != manifest.Digest {
		return ArchiveImageInspection{}, imageRegistryModel.ErrImageTagTaken.Err()
	}
	return ArchiveImageInspection{Reference: ref.String(), Digest: manifest.Digest, Reused: found && have == manifest.Digest}, nil
}

// IndexImageArchive is pass 1 over an image file of a task archive (size bytes, read through open): the image in it,
// or ErrImageArchiveUnreadable with the reason. A failure to read the file itself is a platform error.
func (u *ImageRegistryUseCase) IndexImageArchive(open ociregistry.ArchiveSource, size int64) (*ociregistry.ImageArchive, error) {
	archive, err := ociregistry.ReadImageArchive(open, size, imageRegistryModel.MaxImageArchiveBytes)
	if err != nil {
		var refused *ociregistry.ArchiveError
		if errors.As(err, &refused) {
			return nil, imageArchiveUnreadable(refused.Code)
		}
		if errors.Is(err, ociregistry.ErrSourceCorrupt) {
			return nil, imageArchiveUnreadable(reasonCorrupt)
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the image archive").Err()
	}
	return archive, nil
}

const (
	// reasonDigestMismatch: a blob's bytes in the archive are not its digest.
	reasonDigestMismatch = "digest_mismatch"
	// reasonCorrupt: the archive's container (the zip entry) is broken: longer or shorter than declared, a failed
	// checksum or authentication code.
	reasonCorrupt = "corrupt"
)

// imageArchiveUnreadable is the one place ErrImageArchiveUnreadable is raised.
func imageArchiveUnreadable(reason string) error {
	return imageRegistryModel.ErrImageArchiveUnreadable.WithPublicContext(imageRegistryModel.ContextReason, reason).Err()
}

// PushImageArchive is pass 2: the blobs of the indexed image the registry lacks are streamed into it from the
// archive (each hashed on the way: wrong bytes never complete a blob), then the image is published (publishImage)
// and its platform reference returned. Not configured: ErrRegistryNotConfigured before anything is read.
func (u *ImageRegistryUseCase) PushImageArchive(ctx context.Context, in PushArchiveInput) (CreateImageResult, error) {
	image := CreateImageInput{Name: in.Name, Config: descriptorOf(in.Archive.Config)}
	for _, l := range in.Archive.Layers {
		image.Layers = append(image.Layers, descriptorOf(l))
	}
	// Layer types, counts and the size limit are checked before a byte is pushed.
	if _, err := imageRegistryModel.BuildManifest(image.Config, image.Layers); err != nil {
		return CreateImageResult{}, err
	}
	client, _, err := u.PlatformRegistryClient(ctx, true)
	if err != nil {
		return CreateImageResult{}, err
	}
	want := map[string]bool{}
	for _, d := range append([]Descriptor{image.Config}, image.Layers...) {
		if _, seen := want[d.Digest]; seen {
			continue
		}
		exists, existsErr := client.BlobExists(ctx, d.Digest)
		if existsErr != nil {
			return CreateImageResult{}, Unavailable(existsErr, pushTokenName)
		}
		want[d.Digest] = !exists
	}
	if err = in.Archive.Stream(want, func(blob ociregistry.ArchiveBlob, r io.Reader) error {
		return pushArchiveBlob(ctx, client, blob, r)
	}); err != nil {
		var typed interface{ PublicContext() map[string]any }
		if errors.As(err, &typed) {
			return CreateImageResult{}, err
		}
		if errors.Is(err, ociregistry.ErrSourceCorrupt) {
			return CreateImageResult{}, imageArchiveUnreadable(reasonCorrupt)
		}
		return CreateImageResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the image archive").Err()
	}
	return u.publishImage(ctx, client, in.ExerciseID, in.UploadedBy, image)
}

// pushArchiveBlob streams one blob of the archive into the registry through a verifyingReader.
func pushArchiveBlob(ctx context.Context, client *ociregistry.Client, blob ociregistry.ArchiveBlob, r io.Reader) error {
	vr := &verifyingReader{r: r, h: sha256.New(), want: blob.Digest}
	err := client.PushBlob(ctx, blob.Digest, blob.Size, vr)
	switch {
	case err == nil:
		return nil
	case vr.mismatch:
		return imageArchiveUnreadable(reasonDigestMismatch)
	case errors.Is(vr.sourceErr, ociregistry.ErrSourceCorrupt):
		return imageArchiveUnreadable(reasonCorrupt)
	case ctx.Err() == nil && vr.sourceErr != nil:
		// The archive could not be read (a chunk object gone): not the registry's failure.
		return model.ErrPlatform.WithError(vr.sourceErr).WithMessage("Failed to read the image archive").Err()
	}
	log.Warn().Err(err).Str("digest", blob.Digest).Msg("image archive: blob push failed")
	return Unavailable(err, pushTokenName)
}

func descriptorOf(b ociregistry.ArchiveBlob) Descriptor {
	return Descriptor{MediaType: b.MediaType, Digest: b.Digest, Size: b.Size}
}
