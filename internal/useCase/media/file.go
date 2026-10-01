package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// countingReader tracks how many bytes passed through the use case's own
// stream so an oversized upload is detected even when the client lies about
// Content-Length. This is a USE-CASE-LEVEL guard only — by the time bytes
// reach here, the HTTP handler has already fully parsed the multipart body
// into memory/temp files; it is not a substitute for capping the request
// body size at the HTTP boundary (see the handler's use of
// http.MaxBytesReader before FormFile).
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// UploadFile streams the payload into a temporary S3 object while hashing it,
// then promotes it to the content-addressed blob key (server-side copy,
// skipped when the blob already exists) and records the logical file row.
func (u *MediaUseCase) UploadFile(ctx context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error) {
	if !u.storageConfigured {
		return mediaModel.File{}, mediaModel.ErrMediaStorageNotConfigured.Err()
	}

	fileID := uuid.Must(uuid.NewV7())
	tmpKey := mediaModel.TmpKey(fileID)

	hasher := sha256.New()
	counter := &countingReader{r: io.TeeReader(io.LimitReader(r, u.cfg.MaxUploadBytes+1), hasher)}
	if err := u.storage.Put(ctx, tmpKey, counter, -1, contentType); err != nil {
		return mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to store upload").Err()
	}
	// Best-effort tmp cleanup on every exit path below.
	defer func() { _ = u.storage.Remove(ctx, tmpKey) }()

	if counter.n > u.cfg.MaxUploadBytes {
		return mediaModel.File{}, mediaModel.ErrFileTooLarge.Err()
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	blobKey := mediaModel.BlobKey(hash)
	exists, err := u.storage.Stat(ctx, blobKey)
	if err != nil {
		return mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to stat blob").Err()
	}
	if !exists {
		if err = u.storage.Copy(ctx, tmpKey, blobKey); err != nil {
			return mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to promote blob").Err()
		}
	}

	created, err := u.files.CreateFile(ctx, mediaModel.File{
		ID:          fileID,
		Name:        name,
		ContentType: contentType,
		SizeBytes:   counter.n,
		ContentHash: hash,
		CreatedAt:   time.Now(),
		CreatedBy:   uuid.NullUUID{UUID: createdBy, Valid: createdBy != uuid.Nil},
	})
	if err != nil {
		return mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record file").Err()
	}
	return created, nil
}

// StreamFile opens the blob stream for a logical file. The caller closes the
// reader.
//
// Unconfigured storage is an infrastructure fault here, not a client error:
// file rows only exist because UploadFile once had storage, so hitting this
// guard means the deployment lost its storage config. Reported as a platform
// error (the client-facing ErrMediaStorageNotConfigured keeps its single call
// site in UploadFile).
func (u *MediaUseCase) StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	if !u.storageConfigured {
		return nil, mediaModel.File{}, model.ErrPlatform.WithMessage("File storage is not configured").Err()
	}
	f, err := u.GetFile(ctx, id)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	rc, _, err := u.storage.Get(ctx, mediaModel.BlobKey(f.ContentHash))
	if err != nil {
		return nil, mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to open blob stream").Err()
	}
	return rc, f, nil
}

// GetFile returns a logical file's metadata (no blob access, so it works
// without object storage) — e.g. to validate references and sum sizes.
func (u *MediaUseCase) GetFile(ctx context.Context, id uuid.UUID) (mediaModel.File, error) {
	f, err := u.files.GetFileByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
		}
		return mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get file").Err()
	}
	return f, nil
}
