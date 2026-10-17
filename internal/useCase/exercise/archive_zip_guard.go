package exercise

import (
	"errors"
	"io"
	"sync/atomic"
)

// Both zip readers (archive/zip and yeka/zip) read the whole central directory into memory when they are opened,
// before any entry rule runs, and an archive may be up to 512 MiB + 10 GiB. What an end-of-central-directory record
// declares is not trusted, and no record is parsed by hand (a hand parser may pick another record than the reader
// does). Instead the reader gets a bounded ReaderAt for its opening: the reads of that phase are the end-record
// search (at most the two tail windows of the readers, 1 KiB then 65 KiB + 1 KiB, plus the zip64 records) and the
// central directory, so their sum is capped at zipTailBytes + limits.Entries * maxDirectoryBytesPerEntry. Once over,
// the read fails and the archive is refused with ErrArchiveTooLarge. After the opening the wrapper is a plain
// pass-through, because the entries are read through it later. A zip with no valid end record fails the reader's own
// open and is refused too (nothing is passed through unchecked).
//
// maxDirectoryBytesPerEntry bounds one central directory header: the 46 fixed bytes plus a name, extra field and
// comment of 4 KiB together (paths are far shorter). The directory cap is about 4 MB for the default 1000 entries.
const (
	maxDirectoryBytesPerEntry = 46 + 4096
	zipTailBytes              = 1024 + 65*1024 + 4096
)

var errZipOpenBudget = errors.New("the zip's file list is over the budget")

// openBudgetReaderAt limits the bytes read while a zip reader opens the archive.
type openBudgetReaderAt struct {
	r        io.ReaderAt
	left     atomic.Int64
	done     atomic.Bool
	exceeded atomic.Bool
}

func newOpenBudgetReaderAt(r io.ReaderAt) *openBudgetReaderAt {
	b := &openBudgetReaderAt{r: r}
	b.left.Store(zipTailBytes + int64(limits.Entries)*maxDirectoryBytesPerEntry)
	return b
}

func (b *openBudgetReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if !b.done.Load() && b.left.Add(-int64(len(p))) < 0 {
		b.exceeded.Store(true)
		return 0, errZipOpenBudget
	}
	return b.r.ReadAt(p, off)
}

// finish ends the opening phase: the wrapper passes every read on from now. It returns the refusal when the
// budget was exceeded.
func (b *openBudgetReaderAt) finish() error {
	b.done.Store(true)
	if b.exceeded.Load() {
		return archiveTooLarge("the archive's file list is too large")
	}
	return nil
}
