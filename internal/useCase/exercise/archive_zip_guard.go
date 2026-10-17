package exercise

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Both zip readers (archive/zip and yeka/zip) read the whole central directory into memory before any entry rule
// runs, and an archive may now be up to 512 MiB + 10 GiB. So the end-of-central-directory record (and the zip64
// one when present) is read first, through the ReaderAt, and an archive announcing more entries than the entry cap,
// or a central directory larger than that many entries could fill, is refused before a reader opens it.
//
// maxDirectoryBytesPerEntry bounds one central directory header: the 46 fixed bytes plus a name, extra field and
// comment of 4 KiB together (paths are far shorter; longer ones are refused by the entry rules anyway). The cap is
// limits.Entries * (46 + 4096), about 4 MB for the default 1000 entries.
const maxDirectoryBytesPerEntry = 46 + 4096

const (
	zipEOCDSig       = 0x06054b50
	zipEOCDLen       = 22
	zip64LocatorSig  = 0x07064b50
	zip64LocatorLen  = 20
	zip64EOCDSig     = 0x06064b50
	zip64EOCDMinLen  = 56
	zipMaxCommentLen = 0xFFFF
)

// guardZipDirectory refuses a zip whose end record announces too many entries or too large a central directory.
// A zip with no readable end record passes: the reader names what is wrong with it.
func guardZipDirectory(r io.ReaderAt, size int64) error {
	tail := int64(zipEOCDLen + zipMaxCommentLen)
	if tail > size {
		tail = size
	}
	buf := make([]byte, tail)
	if n, err := r.ReadAt(buf, size-tail); n != len(buf) && err != nil {
		return nil
	}
	pos := -1
	for i := len(buf) - zipEOCDLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(buf[i:]) == zipEOCDSig {
			pos = i
			break
		}
	}
	if pos < 0 {
		return nil
	}
	rec := buf[pos:]
	count := uint64(binary.LittleEndian.Uint16(rec[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(rec[12:]))
	if count == 0xFFFF || dirSize == 0xFFFFFFFF || binary.LittleEndian.Uint32(rec[16:]) == 0xFFFFFFFF {
		eocdAt := size - tail + int64(pos)
		if eocdAt >= zip64LocatorLen {
			loc := make([]byte, zip64LocatorLen)
			if _, err := r.ReadAt(loc, eocdAt-zip64LocatorLen); err == nil && binary.LittleEndian.Uint32(loc) == zip64LocatorSig {
				off := int64(binary.LittleEndian.Uint64(loc[8:]))
				z := make([]byte, zip64EOCDMinLen)
				if off >= 0 && off <= size-zip64EOCDMinLen {
					if _, err = r.ReadAt(z, off); err == nil && binary.LittleEndian.Uint32(z) == zip64EOCDSig {
						count = binary.LittleEndian.Uint64(z[32:])
						dirSize = binary.LittleEndian.Uint64(z[40:])
					}
				}
			}
		}
	}
	if count > uint64(limits.Entries) {
		return archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", limits.Entries))
	}
	if dirSize > uint64(limits.Entries)*maxDirectoryBytesPerEntry {
		return archiveTooLarge("the archive's file list is too large")
	}
	return nil
}
