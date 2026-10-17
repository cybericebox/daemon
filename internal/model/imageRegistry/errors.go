package imageRegistryModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`. Vars without a call site yet are used by the later
// image-registry layers (settings, upload, availability use cases).
//
// ImageRegistryObjectCode — next free detail code: 11
var (
	ErrRegistryNotConfigured = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("The image registry is not configured").WithDetailCode(1)
	// ErrRegistryUnavailable: upload refusal; admins additionally see the reason.
	ErrRegistryUnavailable = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Image upload is unavailable right now: the image registry is not configured or not responding").WithDetailCode(2)
	// ErrImagesUnavailable: deploy refusal when an image cannot be pulled.
	ErrImagesUnavailable = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Image unavailable").WithDetailCode(3)
	ErrInvalidSettings = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid image registry settings").WithDetailCode(4)
	ErrInvalidPlatformRef = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid platform image reference").WithDetailCode(5)
	ErrInvalidEventSecret = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid registry credentials").WithDetailCode(6)
	ErrEventSecretNotFound = err.ErrObjectNotFound.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Registry credentials not found").WithDetailCode(7)
	// ErrSecretsKeyMissing: PlatformCipher is unset, so tokens cannot be sealed.
	ErrSecretsKeyMissing = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Secret storage is not configured: registry tokens cannot be saved").WithDetailCode(8)
	ErrInvalidImageArchive = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid image archive").WithDetailCode(9)
	ErrImageTooLarge = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("The image archive is too large").WithDetailCode(10)
)
