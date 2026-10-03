package mediaModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// MediaObjectCode — next free detail code: 12
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
	// ErrUploadNotFound (category A): the upload does not exist, is somebody else's, or waited too long and was
	// dropped — the client must not tell them apart.
	ErrUploadNotFound = err.ErrObjectNotFound.WithObjectCode(model.MediaObjectCode).
				WithMessage("Upload not found").WithDetailCode(6)
	// ErrUploadChunkOutOfOrder: a chunk other than the next one arrived; the client reads the upload status and
	// continues from the chunk it names.
	ErrUploadChunkOutOfOrder = err.ErrConflict.WithObjectCode(model.MediaObjectCode).
					WithMessage("Chunk out of order").WithDetailCode(7)
	// ErrUploadChunkSize: the chunk is not exactly the size the upload fixed for its index.
	ErrUploadChunkSize = err.ErrInvalidData.WithObjectCode(model.MediaObjectCode).
				WithMessage("Chunk has the wrong size").WithDetailCode(8)
	// ErrUploadIncomplete: the upload was completed before every chunk arrived.
	ErrUploadIncomplete = err.ErrConflict.WithObjectCode(model.MediaObjectCode).
				WithMessage("Upload is not complete").WithDetailCode(9)
	// ErrUploadHashMismatch: the assembled file is not what the client hashed; the upload is dropped.
	ErrUploadHashMismatch = err.ErrInvalidData.WithObjectCode(model.MediaObjectCode).
				WithMessage("Assembled file does not match its hash").WithDetailCode(10)
	// ErrUploadInvalid: a new upload needs a name and a size.
	ErrUploadInvalid = err.ErrInvalidData.WithObjectCode(model.MediaObjectCode).
				WithMessage("The upload needs a name and a size").WithDetailCode(11)
)
