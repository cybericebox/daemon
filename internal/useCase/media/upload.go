package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// maxUploadNameBytes bounds the stored file name.
const maxUploadNameBytes = 255

// StartUpload opens a resumable chunked upload of a file of the given size. The chunks go to PutChunk in order;
// failureReader remembers how its source failed: a read error other than the end of the body, or a body that is
// larger than the limit set at the HTTP boundary (that one is reported as a wrong size, not as a broken link).
type failureReader struct {
	r   io.Reader
	err error
}

func (f *failureReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	var tooLarge *http.MaxBytesError
	if err != nil && !errors.Is(err, io.EOF) && !errors.As(err, &tooLarge) && f.err == nil {
		f.err = err
	}
	return n, err
}

// CompleteUpload assembles them. The purpose sets the size limit: the media file limit for a file, an image
// archive's (imageRegistryModel.MaxImageArchiveBytes) for an image blob, the archive limit in force plus one image
// archive for a task archive.
func (u *MediaUseCase) StartUpload(ctx context.Context, name, contentType string, size int64, createdBy uuid.UUID, purpose mediaModel.UploadPurpose) (mediaModel.Upload, error) {
	if !u.storageConfigured {
		return mediaModel.Upload{}, mediaModel.ErrMediaStorageNotConfigured.Err()
	}
	name = strings.TrimSpace(name)
	limit, known := u.uploadLimit(ctx, purpose)
	if name == "" || len(name) > maxUploadNameBytes || size < 1 || !known {
		return mediaModel.Upload{}, mediaModel.ErrUploadInvalid.Err()
	}
	if size > limit {
		return mediaModel.Upload{}, mediaModel.ErrFileTooLarge.Err()
	}
	upload := mediaModel.NewUpload(uuid.Must(uuid.NewV7()), createdBy, name, contentType, size, u.cfg.UploadChunkBytes, u.cfg.UploadTTL, time.Now(), purpose)
	created, err := u.files.CreateUpload(ctx, upload)
	if err != nil {
		return mediaModel.Upload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to start upload").Err()
	}
	return created, nil
}

// uploadLimit is the largest file an upload of the purpose may carry; known is false for an unknown purpose.
func (u *MediaUseCase) uploadLimit(ctx context.Context, purpose mediaModel.UploadPurpose) (limit int64, known bool) {
	switch purpose {
	case mediaModel.UploadPurposeFile:
		return u.cfg.MaxUploadBytes, true
	case mediaModel.UploadPurposeImageBlob:
		return imageRegistryModel.MaxImageArchiveBytes, true
	case mediaModel.UploadPurposeExerciseArchive:
		if u.archiveUploadLimit != nil {
			return u.archiveUploadLimit(ctx), true
		}
		return exerciseModel.DefaultArchiveLimits().WithImagesBytes(), true
	}
	return 0, false
}

// UploadStatus returns an upload of the caller: where a resumed upload continues.
func (u *MediaUseCase) UploadStatus(ctx context.Context, id, owner uuid.UUID) (mediaModel.Upload, error) {
	upload, err := u.getUpload(ctx, id)
	if err != nil {
		return mediaModel.Upload{}, err
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
	body := &failureReader{r: r}
	counter := &countingReader{r: io.LimitReader(body, want)}
	if err = u.storage.Put(ctx, key, counter, want, "application/octet-stream"); err != nil || counter.n != want {
		_ = u.storage.Remove(ctx, key)
		// A body that broke off (read timeout, dropped link) is retried by the client; only a body that ended
		// cleanly at another size is a wrong size.
		if body.err != nil {
			return mediaModel.Upload{}, mediaModel.ErrUploadChunkInterrupted.WithDetail("cause", body.err.Error()).Err()
		}
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
	// Assembling and promoting a big file must not depend on the client connection (a dropped client or a
	// request deadline would leave a half-promoted blob): it runs on its own context with its own timeout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mediaModel.LongOperationTimeout)
	defer cancel()
	upload, err := u.UploadStatus(ctx, id, owner)
	if err != nil {
		return mediaModel.File{}, err
	}
	// An image blob goes to the registry, a task archive is read where its chunks lie: neither becomes a file.
	if upload.Purpose != mediaModel.UploadPurposeFile {
		return mediaModel.File{}, mediaModel.ErrUploadNotAFile.Err()
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

// OpenUpload streams the chunks of an upload in index order as one reader; the caller closes it. There is no
// owner check: it serves the background image-blob push, whose request already checked the caller.
func (u *MediaUseCase) OpenUpload(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.Upload, error) {
	upload, err := u.getUpload(ctx, id)
	if err != nil {
		return nil, mediaModel.Upload{}, err
	}
	return &chunkReader{ctx: ctx, storage: u.storage, upload: upload}, upload, nil
}

// DropUpload drops an upload with its chunks, whoever started it (the image-blob push once it is done).
func (u *MediaUseCase) DropUpload(ctx context.Context, id uuid.UUID) error {
	upload, err := u.getUpload(ctx, id)
	if err != nil {
		return err
	}
	u.dropUpload(ctx, upload)
	return nil
}

// getUpload loads an upload by id only.
func (u *MediaUseCase) getUpload(ctx context.Context, id uuid.UUID) (mediaModel.Upload, error) {
	upload, err := u.files.GetUpload(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return mediaModel.Upload{}, mediaModel.ErrUploadNotFound.Err()
		}
		return mediaModel.Upload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get upload").Err()
	}
	return upload, nil
}

// chunkReader reads the chunk objects of an upload one after another, opening each only when the previous one
// is used up, so a large upload is streamed and never held whole.
type chunkReader struct {
	ctx     context.Context
	storage IStorage
	upload  mediaModel.Upload
	next    int
	cur     io.ReadCloser
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for {
		if c.cur == nil {
			if c.next >= c.upload.ChunkCount() {
				return 0, io.EOF
			}
			rc, _, err := c.storage.Get(c.ctx, mediaModel.ChunkKey(c.upload.ID, c.next))
			if err != nil {
				return 0, err
			}
			c.cur, c.next = rc, c.next+1
		}
		n, err := c.cur.Read(p)
		if errors.Is(err, io.EOF) {
			_ = c.cur.Close()
			c.cur, err = nil, nil
		}
		if n > 0 || err != nil {
			return n, err
		}
	}
}

func (c *chunkReader) Close() error {
	if c.cur != nil {
		return c.cur.Close()
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
