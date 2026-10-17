package media

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
)

// readAheadBytes is how much one range request fetches at most: a zip reader reads a few KiB at a time, and a
// request per read would be millions of requests for a large archive.
const readAheadBytes = 8 << 20

// OpenUploadAt gives random access to the bytes of an upload, read where its chunk objects lie (range requests),
// never assembled: a task archive import reads its zip this way. There is no owner check: the import job reads it
// after its request checked the caller. Reads use ctx.
func (u *MediaUseCase) OpenUploadAt(ctx context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.Upload, error) {
	upload, err := u.getUpload(ctx, id)
	if err != nil {
		return nil, mediaModel.Upload{}, err
	}
	parts := make([]objectPart, upload.ChunkCount())
	for i := range parts {
		parts[i] = objectPart{key: mediaModel.ChunkKey(upload.ID, i), size: upload.ChunkSize(i)}
	}
	return newObjectReaderAt(ctx, u.storage, parts), upload, nil
}

// OpenFileAt gives random access to a stored file's bytes (range requests on its blob). No owner check: callers
// check the file's owner first.
func (u *MediaUseCase) OpenFileAt(ctx context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.File, error) {
	if !u.storageConfigured {
		return nil, mediaModel.File{}, model.ErrPlatform.WithMessage("File storage is not configured").Err()
	}
	f, err := u.GetFile(ctx, id)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	parts := []objectPart{{key: mediaModel.BlobKey(f.ContentHash), size: f.SizeBytes}}
	return newObjectReaderAt(ctx, u.storage, parts), f, nil
}

// objectPart is one stored object of a byte sequence.
type objectPart struct {
	key  string
	size int64
}

// objectReaderAt is io.ReaderAt over objects laid end to end. It keeps one read-ahead window (within one object)
// so sequential small reads cost one range request per readAheadBytes. Safe for concurrent use.
type objectReaderAt struct {
	ctx     context.Context
	storage IStorage
	parts   []objectPart
	starts  []int64
	size    int64

	mu     sync.Mutex
	window []byte
	from   int64 // offset of window[0] in the whole sequence
}

func newObjectReaderAt(ctx context.Context, storage IStorage, parts []objectPart) *objectReaderAt {
	r := &objectReaderAt{ctx: ctx, storage: storage, parts: parts, starts: make([]int64, len(parts))}
	for i, p := range parts {
		r.starts[i] = r.size
		r.size += p.size
	}
	return r
}

func (r *objectReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset %d", off)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for n < len(p) {
		at := off + int64(n)
		if at >= r.size {
			return n, io.EOF
		}
		if at < r.from || at >= r.from+int64(len(r.window)) {
			if err := r.fill(at, int64(len(p)-n)); err != nil {
				return n, err
			}
		}
		n += copy(p[n:], r.window[at-r.from:])
	}
	return n, nil
}

// fill loads the window starting at off: at least want bytes (up to the end of off's object), at least
// readAheadBytes when the object has them.
func (r *objectReaderAt) fill(off, want int64) error {
	i := len(r.starts) - 1
	for i > 0 && r.starts[i] > off {
		i--
	}
	within := off - r.starts[i]
	length := min(max(want, readAheadBytes), r.parts[i].size-within)
	rc, err := r.storage.GetRange(r.ctx, r.parts[i].key, within, length)
	if err != nil {
		return fmt.Errorf("read %s: %w", r.parts[i].key, err)
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, length)
	if _, err = io.ReadFull(rc, buf); err != nil {
		return fmt.Errorf("read %s: %w", r.parts[i].key, err)
	}
	r.window, r.from = buf, off
	return nil
}
