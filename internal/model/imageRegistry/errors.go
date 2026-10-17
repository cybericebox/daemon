package imageRegistryModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`. Vars without a call site yet are used by the later
// image-registry layers (settings, upload, availability use cases).
//
// ImageRegistryObjectCode — next free detail code: 31
var (
	ErrRegistryNotConfigured = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("The image registry is not configured").WithDetailCode(1)
	// ErrRegistryUnavailable: upload refusal; admins additionally see the reason.
	ErrRegistryUnavailable = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Image uploads are unavailable right now: the image registry is not configured or not responding").WithDetailCode(2)
	// ErrImagesUnavailable: deploy refusal when an image cannot be pulled.
	ErrImagesUnavailable = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Image unavailable").WithDetailCode(3)
	ErrInvalidSettings = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid image registry settings").WithDetailCode(4)
	ErrInvalidPlatformRef = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid platform image reference").WithDetailCode(5)
	// ErrInvalidEventSecret: the host, repository prefix or username of an event secret is not valid.
	ErrInvalidEventSecret = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid registry credentials").WithDetailCode(6)
	// ErrEventSecretNotFound — multi-site (category A, anti-enumeration): an event secret is always read,
	// changed and deleted within its event, so an absent id, another event's secret and one deleted
	// concurrently are one indistinguishable "not found" across update and delete.
	ErrEventSecretNotFound = err.ErrObjectNotFound.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Registry credentials not found").WithDetailCode(7)
	// ErrSecretsKeyMissing: PlatformCipher is unset, so the platform registry tokens cannot be sealed.
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
	// ErrBlobDigestMismatch: the upload's size is not the blob's the client named; nothing is pushed (a wrong
	// sha256 is found by the background push: state failed, reason digest_mismatch).
	ErrBlobDigestMismatch = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("The uploaded layer does not match its digest or size").WithDetailCode(16)
	// ErrBlobMissing: an image names a blob the registry does not have (public context "digest"); upload it first.
	ErrBlobMissing = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
			WithMessage("An image layer is not uploaded yet").WithDetailCode(17)
	// ErrStagedBlobNotFound (category A, anti-enumeration): the upload does not exist, expired or belongs to
	// another user; the client cannot tell them apart.
	ErrStagedBlobNotFound = err.ErrObjectNotFound.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("The uploaded layer was not found").WithDetailCode(18)
	ErrInvalidBlobDigest = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Invalid layer digest").WithDetailCode(19)
	// ErrBlobUploadNotReady: the upload named for a blob push is not an image-layer upload, or not all its chunks
	// arrived yet.
	ErrBlobUploadNotReady = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("The layer upload is not complete").WithDetailCode(20)
	// ErrBlobSizeMismatch: an image names a blob with a size other than the registry's (public context "digest").
	ErrBlobSizeMismatch = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("An image layer has a different size than stated").WithDetailCode(21)
	// ErrInvalidImageConfig: the image config is not linux/amd64, does not list one diff id per layer, cannot be
	// read, or is larger than MaxImageConfigBytes.
	ErrInvalidImageConfig = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("The image must be a linux/amd64 image with a valid config").WithDetailCode(22)
	// ErrUploaderUnknown: an image upload call without a signed-in caller.
	ErrUploaderUnknown = err.ErrUnauthenticated.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Sign in to upload images").WithDetailCode(23)
	// ErrInvalidEventSecretEvent: an event secret was built for an event id that is not a UUID.
	ErrInvalidEventSecretEvent = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("Invalid event for registry credentials").WithDetailCode(24)
	// ErrEventSecretTokenMissing: a new event secret needs its token.
	ErrEventSecretTokenMissing = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("Enter the registry token").WithDetailCode(25)
	// ErrEventSecretKeyMissing: PlatformCipher is unset, so a new event secret's token cannot be sealed.
	ErrEventSecretKeyMissing = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("Secret storage is not configured: registry tokens cannot be saved").WithDetailCode(26)
	// ErrEventSecretTokenKeyMissing: PlatformCipher is unset, so a new token for an event secret cannot be sealed
	// (an edit without a new token still works).
	ErrEventSecretTokenKeyMissing = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("Secret storage is not configured: registry tokens cannot be saved").WithDetailCode(27)
	// ErrImageTagTaken: the platform tag of an image is already recorded for another digest. Tags carry the whole
	// digest, so this means a bug; the upload is refused rather than re-point a tag another image may serve.
	ErrImageTagTaken = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("Another image already uses this registry tag").WithDetailCode(28)
	// ErrImageArchiveUnreadable: an image file of a task archive is not one linux/amd64 image in an uncompressed
	// docker save tar, is over MaxImageArchiveBytes, or a blob's bytes are not its digest. The public context
	// "reason" says which: gzip, not_tar, no_image, multi_image, no_amd64, too_large, digest_mismatch.
	ErrImageArchiveUnreadable = err.ErrInvalidData.WithObjectCode(model.ImageRegistryObjectCode).
					WithMessage("The image archive cannot be imported").WithDetailCode(29)
	// ErrImageNotExportable: a platform image of a task cannot be written into an export «з образами»: the registry
	// is not configured, refused, failed, or does not have the image. The public context "reason" says which
	// (not_configured, not_found, unauthorized, rate_limited, unreachable, invalid_reference, invalid_image); the
	// export adds "device" and "file".
	ErrImageNotExportable = err.ErrConflict.WithObjectCode(model.ImageRegistryObjectCode).
				WithMessage("A platform image cannot be exported: the image registry is not configured, not responding or does not have it").WithDetailCode(30)
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
	// ContextDigest is the public error context of ErrBlobMissing and ErrBlobSizeMismatch: the blob's digest.
	ContextDigest = "digest"
)
