package imageRegistryModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`. Vars without a call site yet are used by the later
// image-registry layers (settings, upload, availability use cases).
//
// ImageRegistryObjectCode — next free detail code: 16
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
	// ErrPushTokenRequired: the first save (or a new provider account) needs a push token.
	ErrPushTokenRequired = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Enter the registry token").WithDetailCode(11)
	ErrInvalidRepositoryListing = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("Choose the provider and enter the account name").WithDetailCode(12)
	// ErrConflictingPullToken: a new pull token and "remove the pull token" in one save.
	ErrConflictingPullToken = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Either enter a new pull token or remove it, not both").WithDetailCode(13)
	// ErrDuplicateEventSecret: the event already has a secret for this host and repository prefix.
	ErrDuplicateEventSecret = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("The event already has registry credentials for this host and repository prefix").WithDetailCode(14)
	// ErrEventSecretTokenRequired: a secret moved to another host must get a new token, never reuse the stored one.
	ErrEventSecretTokenRequired = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("Enter the token again when the registry host changes").WithDetailCode(15)
)

// ContextReason is the public error context of ErrRegistryUnavailable naming
// why the registry failed (a Reason value); shown to platform admins.
// ContextToken names which token failed ("push" or "pull"), never its value.
//
// ContextImages is the public error context of ErrImagesUnavailable: the list
// of unavailable images ([]Availability, JSON objects {Image, Status, Reason}),
// so the client can name each image and why. It never carries a token.
const (
	ContextReason = "reason"
	ContextToken  = "token"
	ContextImages = "images"
)
