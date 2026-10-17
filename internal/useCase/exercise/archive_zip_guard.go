package exercise

import (
	"encoding/binary"
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
	if b.done.Load() {
		return b.r.ReadAt(p, off)
	}
	if b.left.Add(-int64(len(p))) < 0 {
		b.exceeded.Store(true)
		return 0, errZipOpenBudget
	}
	n, err := b.r.ReadAt(p, off)
	// The readers size their file list from the DECLARED record count before reading one directory byte (a 10 GiB
	// archive declaring 200M entries makes them allocate over a GiB). So the bytes about to be parsed are looked at
	// first, and the read fails when an end record in them declares more entries than the cap.
	if declaresTooManyEntries(p[:n]) {
		b.exceeded.Store(true)
		return 0, errZipOpenBudget
	}
	return n, err
}

const (
	eocdSignature   = 0x06054b50 // end of central directory: entries on this disk at +8, in total at +10
	eocd64Signature = 0x06064b50 // zip64 end record: entries on this disk at +24, in total at +32
	eocdMinLen      = 22
	eocd64MinLen    = 56
)

// declaresTooManyEntries scans every position of buf (not only the last candidate: the readers' pick cannot differ
// from a scan of all of them) for an end record whose entry counts exceed limits.Entries. The 0xFFFF of a plain
// record is the zip64 sentinel (Info-ZIP and 7z saturate it for any zip64 archive, e.g. images over 4 GiB), not a
// count: the real one is in the zip64 record, which the scan checks as well. The same goes for the declared
// directory size against the directory byte cap (0xFFFFFFFF is the plain record's sentinel).
func declaresTooManyEntries(buf []byte) bool {
	max := uint64(limits.Entries)
	maxDir := max * maxDirectoryBytesPerEntry
	for i := 0; i+4 <= len(buf); i++ {
		switch binary.LittleEndian.Uint32(buf[i:]) {
		case eocdSignature:
			if i+eocdMinLen <= len(buf) &&
				(plainCountOver(binary.LittleEndian.Uint16(buf[i+8:]), max) || plainCountOver(binary.LittleEndian.Uint16(buf[i+10:]), max) ||
					plainSizeOver(binary.LittleEndian.Uint32(buf[i+12:]), maxDir)) {
				return true
			}
		case eocd64Signature:
			if i+eocd64MinLen <= len(buf) &&
				(binary.LittleEndian.Uint64(buf[i+24:]) > max || binary.LittleEndian.Uint64(buf[i+32:]) > max ||
					binary.LittleEndian.Uint64(buf[i+40:]) > maxDir) {
				return true
			}
		}
	}
	return false
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

func plainCountOver(count uint16, max uint64) bool { return count != 0xFFFF && uint64(count) > max }

func plainSizeOver(size uint32, max uint64) bool { return size != 0xFFFFFFFF && uint64(size) > max }
