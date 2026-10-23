package exercise

import (
	"context"
	"errors"
	"io"
	"sort"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// Export «з образами»: the platform images of a task's devices are written into the archive as image archives
// (OCI layout + docker load manifest.json) under images/, placed by the same rule as attachments (assignExportPaths),
// one file per image per task however many devices use it. The document names the file: `image: {file: …}`.

type (
	// ExportedImage is one platform image ready to be written as an image archive (the image registry use case).
	ExportedImage interface {
		// Digest is the image's manifest digest.
		Digest() string
		// Size is the exact length of the archive WriteTo writes.
		Size() int64
		// WriteTo streams the archive into w. A registry failure is a typed error with its reason.
		WriteTo(ctx context.Context, w io.Writer) error
	}

	// IImageExporter reads platform images for an export (the image registry use case).
	IImageExporter interface {
		// ExportedImage resolves a platform reference ("platform/<name>@sha256:…") to the image to write.
		ExportedImage(ctx context.Context, reference string) (ExportedImage, error)
	}

	// exportedImage is one image file of an export: where it sits in the task folder and the device it is named after.
	exportedImage struct {
		Path   string
		Device string
		Image  ExportedImage
	}
)

// exportImages resolves and places the platform images of the task's devices. It returns the image files and, per
// manifest digest, the path the document links. A linked variant written compact places only its own device images.
func (u *ExerciseUseCase) exportImages(ctx context.Context, versions []exerciseModel.ExerciseVersion) ([]exportedImage, map[string]string, error) {
	var uses []fileUse
	firstRef := map[string]string{}    // digest → the reference of its first device
	firstDevice := map[string]string{} // digest → the name of its first device
	for vi := range versions {
		for xi, variant := range versions[vi].Variants {
			// A linked variant written compact places only its own images (device_images).
			_, compact := exportLinkedVariantV2(variant, versions[vi].Variants, nil, nil)
			own := map[uuid.UUID]bool{}
			for _, override := range variant.ImageOverrides {
				own[override.DeviceID] = true
			}
			for di, device := range variant.Topology.Devices {
				if compact && !own[device.ID] {
					continue
				}
				ref, ok := imageRegistryModel.ParsePlatformRef(device.Image)
				if !ok {
					continue
				}
				uses = append(uses, fileUse{Hash: ref.Digest, Name: device.Name + ".tar", Version: vi + 1, Variant: xi + 1, Device: di + 1})
				if _, seen := firstRef[ref.Digest]; !seen {
					firstRef[ref.Digest], firstDevice[ref.Digest] = device.Image, device.Name
				}
			}
		}
	}
	if len(uses) == 0 {
		return nil, nil, nil
	}
	if u.imageExporter == nil {
		return nil, nil, model.ErrPlatform.WithMessage("Image export is not wired").Err()
	}
	paths := assignExportPaths("images", uses)
	digests := make([]string, 0, len(paths))
	for digest := range paths {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	images := make([]exportedImage, 0, len(digests))
	var total int64
	for _, digest := range digests {
		image, err := u.imageExporter.ExportedImage(ctx, firstRef[digest])
		if err != nil {
			return nil, nil, withExportedImage(err, firstDevice[digest], paths[digest])
		}
		if total += image.Size(); total > imageRegistryModel.MaxImageArchiveBytes {
			return nil, nil, archiveTooLarge("the images are over " + gib(imageRegistryModel.MaxImageArchiveBytes) + " together")
		}
		images = append(images, exportedImage{Path: paths[digest], Device: firstDevice[digest], Image: image})
	}
	return images, paths, nil
}

// withExportedImage adds the device and the archive path to a refusal to export an image, as its public context.
func withExportedImage(failure error, device, file string) error {
	var creator appErr.ErrorCreator
	if !errors.As(failure, &creator) {
		return failure
	}
	return creator.WithPublicContext("device", device).WithPublicContext("file", file).Err()
}
