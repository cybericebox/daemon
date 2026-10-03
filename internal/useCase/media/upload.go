package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// maxUploadNameBytes bounds the stored file name.
const maxUploadNameBytes = 255

// StartUpload opens a resumable chunked upload of a file of the given size. The chunks go to PutChunk in order;
// CompleteUpload assembles them.
func (u *MediaUseCase) StartUpload(ctx context.Context, name, contentType string, size int64, createdBy uuid.UUID) (mediaModel.Upload, error) {
	if !u.storageConfigured {
		return mediaModel.Upload{}, mediaModel.ErrMediaStorageNotConfigured.Err()
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxUploadNameBytes || size < 1 {
		return mediaModel.Upload{}, mediaModel.ErrUploadInvalid.Err()
	}
	if size > u.cfg.MaxUploadBytes {
		return mediaModel.Upload{}, mediaModel.ErrFileTooLarge.Err()
	}
	upload := mediaModel.NewUpload(uuid.Must(uuid.NewV7()), createdBy, name, contentType, size, u.cfg.UploadChunkBytes, u.cfg.UploadTTL, time.Now())
	created, err := u.files.CreateUpload(ctx, upload)
	if err != nil {
		return mediaModel.Upload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to start upload").Err()
	}
	return created, nil
}

// UploadStatus returns an upload of the caller: where a resumed upload continues.
func (u *MediaUseCase) UploadStatus(ctx context.Context, id, owner uuid.UUID) (mediaModel.Upload, error) {
	upload, err := u.files.GetUpload(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
		}
		return mediaModel.Upload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get upload").Err()
	}
	// Somebody else's and expired uploads are not found: the caller cannot tell them from an absent one.
	if !upload.OwnedBy(owner) || upload.Expired(time.Now()) {
		return mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
	}
	return upload, nil
}

// PutChunk stores chunk index (0-based) of an upload. Chunks arrive in order; one that was already received (the
// answer to the last request was lost) is accepted again without storing it, so a client may always repeat a chunk.
func (u *MediaUseCase) PutChunk(ctx context.Context, id, owner uuid.UUID, index int, r io.Reader) (mediaModel.Upload, error) {
	upload, err := u.UploadStatus(ctx, id, owner)
	if err != nil {
		return mediaModel.Upload{}, err
	}
	if index < upload.ChunksReceived {
		return upload, nil
	}
	if index != upload.ChunksReceived || index >= upload.ChunkCount() {
		return mediaModel.Upload{}, mediaModel.ErrUploadChunkOutOfOrder.Err()
	}
	want := upload.ChunkSize(index)
	key := mediaModel.ChunkKey(id, index)
	counter := &countingReader{r: io.LimitReader(r, want)}
	if err = u.storage.Put(ctx, key, counter, want, "application/octet-stream"); err != nil || counter.n != want {
		_ = u.storage.Remove(ctx, key)
		if err == nil || errors.Is(err, io.ErrUnexpectedEOF) || counter.n != want {
			return mediaModel.Upload{}, mediaModel.ErrUploadChunkSize.Err()
		}
		return mediaModel.Upload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to store chunk").Err()
	}
	// A body longer than the chunk is a wrong size too.
	var extra [1]byte
	if n, _ := r.Read(extra[:]); n > 0 {
		_ = u.storage.Remove(ctx, key)
		return mediaModel.Upload{}, mediaModel.ErrUploadChunkSize.Err()
	}
	now := time.Now()
	affected, err := u.files.AdvanceUpload(ctx, id, owner, index, now.Add(u.cfg.UploadTTL))
	if err != nil {
		return mediaModel.Upload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record chunk").Err()
	}
	if affected == 0 {
		// A concurrent request for the same chunk won; the client reads the status and carries on.
		return mediaModel.Upload{}, mediaModel.ErrUploadChunkOutOfOrder.Err()
	}
	upload.ChunksReceived++
	upload.ExpiresAt = now.Add(u.cfg.UploadTTL)
	return upload, nil
}

// CompleteUpload assembles the chunks into one object while hashing it, checks the size and the sha256 the client
// computed, and records the file as UploadFile would. A file that does not match its hash is dropped with its
// chunks: the client starts again.
func (u *MediaUseCase) CompleteUpload(ctx context.Context, id, owner uuid.UUID, wantSHA256 string) (mediaModel.File, error) {
	upload, err := u.UploadStatus(ctx, id, owner)
	if err != nil {
		return mediaModel.File{}, err
	}
	if !upload.Complete() {
		return mediaModel.File{}, mediaModel.ErrUploadIncomplete.Err()
	}
	fileID := uuid.Must(uuid.NewV7())
	tmpKey := mediaModel.TmpKey(fileID)

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(u.copyChunks(ctx, upload, pw)) }()
	hasher := sha256.New()
	counter := &countingReader{r: io.TeeReader(pr, hasher)}
	putErr := u.storage.Put(ctx, tmpKey, counter, upload.SizeBytes, upload.ContentType)
	_ = pr.Close()
	// Best-effort tmp cleanup on every exit path below.
	defer func() { _ = u.storage.Remove(ctx, tmpKey) }()
	if putErr != nil {
		return mediaModel.File{}, model.ErrPlatform.WithError(putErr).WithMessage("Failed to assemble upload").Err()
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	if counter.n != upload.SizeBytes || !strings.EqualFold(hash, strings.TrimSpace(wantSHA256)) {
		u.dropUpload(ctx, upload)
		return mediaModel.File{}, mediaModel.ErrUploadHashMismatch.Err()
	}
	file, err := u.publish(ctx, fileID, tmpKey, upload.Name, upload.ContentType, counter.n, hash, owner)
	if err != nil {
		return mediaModel.File{}, err
	}
	u.dropUpload(ctx, upload)
	return file, nil
}

// copyChunks writes the chunks of an upload to w in order.
func (u *MediaUseCase) copyChunks(ctx context.Context, upload mediaModel.Upload, w io.Writer) error {
	for i := 0; i < upload.ChunkCount(); i++ {
		rc, _, err := u.storage.Get(ctx, mediaModel.ChunkKey(upload.ID, i))
		if err != nil {
			return err
		}
		_, err = io.Copy(w, rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// AbortUpload drops an upload of the caller with its chunks.
func (u *MediaUseCase) AbortUpload(ctx context.Context, id, owner uuid.UUID) error {
	upload, err := u.UploadStatus(ctx, id, owner)
	if err != nil {
		return err
	}
	u.dropUpload(ctx, upload)
	return nil
}

// dropUpload removes the chunk objects and the row. Best effort: what a failure leaves, the expiry cleanup takes.
func (u *MediaUseCase) dropUpload(ctx context.Context, upload mediaModel.Upload) {
	for i := 0; i < upload.ChunkCount(); i++ {
		if err := u.storage.Remove(ctx, mediaModel.ChunkKey(upload.ID, i)); err != nil {
			log.Warn().Err(err).Str("upload", upload.ID.String()).Msg("media: chunk removal failed, the cleanup will retry")
			return
		}
	}
	if _, err := u.files.DeleteUpload(ctx, upload.ID); err != nil {
		log.Warn().Err(err).Str("upload", upload.ID.String()).Msg("media: upload row removal failed, the cleanup will retry")
	}
}

// cleanupBatch is how many expired uploads one pass drops.
const cleanupBatch = 100

// CleanupExpiredUploads drops the uploads that waited too long for their next chunk, with the chunks stored so far.
func (u *MediaUseCase) CleanupExpiredUploads(ctx context.Context) error {
	if !u.storageConfigured {
		return nil
	}
	expired, err := u.files.ListExpiredUploads(ctx, time.Now(), cleanupBatch)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list expired uploads").Err()
	}
	for _, upload := range expired {
		u.dropUpload(ctx, upload)
	}
	return nil
}
