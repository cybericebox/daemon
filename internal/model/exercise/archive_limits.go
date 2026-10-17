package exerciseModel

import imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"

const (
	// MaxArchiveBytes is the largest task archive (.zip) without device images.
	MaxArchiveBytes int64 = 512 << 20
	// MaxArchiveWithImagesBytes is the largest task archive that carries device images (`image: {file: …}`): the
	// archive limit plus one image archive's (all its images together are at most MaxImageArchiveBytes).
	MaxArchiveWithImagesBytes = MaxArchiveBytes + imageRegistryModel.MaxImageArchiveBytes
)
