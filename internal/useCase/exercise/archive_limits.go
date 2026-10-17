package exercise

import (
	"compress/flate"
	"fmt"
	"io"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Archive limits, one set for import and export: the export refuses what the import would refuse, so an exported
// task imports again. One entry may be as large as one stored file (MEDIA_MAX_UPLOAD_BYTES, 512 MiB by default),
// so every attachment fits. To be moved to platform settings later.
const (
	// MaxArchiveBytes is the .zip file itself.
	MaxArchiveBytes int64 = 512 << 20
	// MaxArchiveEntryBytes is one file unpacked.
	MaxArchiveEntryBytes int64 = 512 << 20
	// MaxArchiveUnpackedBytes is all files unpacked together.
	MaxArchiveUnpackedBytes int64 = 512 << 20
	// MaxArchiveEntries counts every zip entry, folders and manifest.json included.
	MaxArchiveEntries = 1000
	// A file over archiveRatioFreeBytes may not expand more than maxArchiveRatio times: a small archive of
	// repeated bytes unpacks into gigabytes otherwise.
	maxArchiveRatio       = 200
	archiveRatioFreeBytes = 1 << 20
)

type archiveLimits struct {
	Archive, Entry, Unpacked int64
	Entries                  int
}

// limits are the limits in force: the constants above (tests swap in small ones).
var limits = archiveLimits{Archive: MaxArchiveBytes, Entry: MaxArchiveEntryBytes, Unpacked: MaxArchiveUnpackedBytes, Entries: MaxArchiveEntries}

// archiveTooLarge is the one place ErrArchiveTooLarge is raised.
func archiveTooLarge(reason string) error {
	return exerciseModel.ErrArchiveTooLarge.WithPublicContext("reason", reason).Err()
}

func mib(n int64) string { return fmt.Sprintf("%d MiB", n>>20) }

// checkArchiveSize refuses a zip over the archive cap.
func checkArchiveSize(size int) error {
	if int64(size) > limits.Archive {
		return archiveTooLarge("the archive is over " + mib(limits.Archive))
	}
	return nil
}

// ReadArchiveBytes reads an uploaded archive whole, refusing one over the archive cap (never cutting it).
func ReadArchiveBytes(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limits.Archive+1))
	if err != nil {
		return nil, err
	}
	if err = checkArchiveSize(len(body)); err != nil {
		return nil, err
	}
	return body, nil
}

// archiveBudget tracks the entries and bytes of one archive, read or written.
type archiveBudget struct {
	total   int64
	entries int
}

// check refuses an entry the zip header already shows to be too large or expanding too far, before it is read.
func (b *archiveBudget) check(entries int, uncompressed, compressed uint64) error {
	if entries > limits.Entries {
		return archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", limits.Entries))
	}
	if uncompressed > uint64(limits.Entry) {
		return archiveTooLarge("a file is over " + mib(limits.Entry) + " unpacked")
	}
	if uncompressed > archiveRatioFreeBytes && (compressed == 0 || uncompressed/compressed > maxArchiveRatio) {
		return archiveTooLarge(fmt.Sprintf("a file expands more than %d times", maxArchiveRatio))
	}
	return nil
}

// read unpacks one entry within the limits; the header's sizes are not trusted, the bytes actually read count.
func (b *archiveBudget) read(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limits.Entry+1))
	if err != nil {
		return nil, err
	}
	if err = b.add(len(body)); err != nil {
		return nil, err
	}
	return body, nil
}

// add counts one entry of size bytes: the entry and the unpacked total caps.
func (b *archiveBudget) add(size int) error {
	b.entries++
	if int64(size) > limits.Entry {
		return archiveTooLarge("a file is over " + mib(limits.Entry) + " unpacked")
	}
	if b.total += int64(size); b.total > limits.Unpacked {
		return archiveTooLarge("the files are over " + mib(limits.Unpacked) + " unpacked together")
	}
	return nil
}

// checkExportEntries refuses an archive to be written with these files and a manifest.json beside them that the
// import would refuse.
func checkExportEntries(files map[string][]byte) error {
	if len(files)+1 > limits.Entries {
		return archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", limits.Entries))
	}
	var budget archiveBudget
	for _, body := range files {
		if err := budget.add(len(body)); err != nil {
			return err
		}
	}
	return nil
}

// expandsTooFar tells whether the deflated body would break the import's ratio rule (check): such a body is
// written uncompressed (zip Store), so an archive the export writes never fails it. Level 5 is what archive/zip and
// yeka/zip deflate with, so the size counted here is the size written; an AES entry is only larger.
func expandsTooFar(body []byte) bool {
	if len(body) <= archiveRatioFreeBytes {
		return false
	}
	var counted countingWriter
	fw, err := flate.NewWriter(&counted, 5)
	if err != nil {
		return true
	}
	if _, err = fw.Write(body); err != nil || fw.Close() != nil {
		return true
	}
	return counted == 0 || int64(len(body))/int64(counted) > maxArchiveRatio
}

// countingWriter counts the bytes written to it and keeps none.
type countingWriter int64

func (c *countingWriter) Write(p []byte) (int, error) {
	*c += countingWriter(len(p))
	return len(p), nil
}
