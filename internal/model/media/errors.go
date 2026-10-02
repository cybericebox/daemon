package mediaModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// MediaObjectCode — next free detail code: 6
var (
	// multi-site (category A): the media use case (no such file row) and the
	// email template image route (a file that is not a template image) — the
	// client must not tell a foreign media file from an absent one.
	ErrFileNotFound = err.ErrObjectNotFound.WithObjectCode(model.MediaObjectCode).
			WithMessage("File not found").WithDetailCode(1)
	// multi-site: the use case's own post-hash size check (UploadFile) and the
	// exercise files handler's HTTP-boundary check (http.MaxBytesReader ahead
	// of multipart parsing) both report the same public fact — "the upload
	// exceeds the configured cap" — just caught at two different points in
	// the same request's lifecycle. Neither site is security-sensitive, so
	// sharing one detail code is fine (anti-enumeration doesn't apply here).
	ErrFileTooLarge = err.ErrInvalidData.WithObjectCode(model.MediaObjectCode).
			WithMessage("File exceeds the maximum upload size").WithDetailCode(2)
	ErrMediaStorageNotConfigured = err.ErrConflict.WithObjectCode(model.MediaObjectCode).
					WithMessage("File storage is not configured").WithDetailCode(3)
	// ErrImageTooLarge: the picture has more pixels than IMAGE_MAX_PIXELS: a small file can hold a huge canvas
	// that stops the tab of everyone who views it, and costs memory to scale for mail.
	ErrImageTooLarge = err.ErrInvalidData.WithObjectCode(model.MediaObjectCode).
				WithMessage("The picture has too many pixels").WithDetailCode(4)
	// ErrImageInvalid: the picture's header cannot be read.
	ErrImageInvalid = err.ErrInvalidData.WithObjectCode(model.MediaObjectCode).
			WithMessage("The picture cannot be read").WithDetailCode(5)
)
