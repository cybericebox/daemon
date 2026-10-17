package exercise

import (
	"compress/flate"
	"context"
	"fmt"
	"io"

	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The archive limits are the platform settings' (exerciseModel.ArchiveLimits, read once per import or export and
// passed down, so one archive is never checked against two sets). The ratio rule below is fixed.
const (
	// A file over archiveRatioFreeBytes may not expand more than maxArchiveRatio times: a small archive of
	// repeated bytes unpacks into gigabytes otherwise.
	maxArchiveRatio       = 200
	archiveRatioFreeBytes = 1 << 20
)

// ArchiveLimits are the task archive limits in force: the platform settings, or the defaults when they cannot be
// read (logged). Read once per import or export and passed down.
func (u *ExerciseUseCase) ArchiveLimits(ctx context.Context) exerciseModel.ArchiveLimits {
	if u.settings == nil {
		return exerciseModel.DefaultArchiveLimits()
	}
	lim, err := u.settings.ArchiveLimits(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Archive limits cannot be read, the defaults apply")
		return exerciseModel.DefaultArchiveLimits()
	}
	return lim
}

// archiveTooLarge is the one place ErrArchiveTooLarge is raised.
func archiveTooLarge(reason string) error {
	return exerciseModel.ErrArchiveTooLarge.WithPublicContext("reason", reason).Err()
}

func mib(n int64) string { return fmt.Sprintf("%d MiB", n>>20) }

// checkArchiveSize refuses a zip over the archive cap.
func checkArchiveSize(size int64, maxBytes int64) error {
	if size > maxBytes {
		return archiveTooLarge("the archive is over " + mib(maxBytes))
	}
	return nil
}

// ReadArchiveBytes reads an uploaded archive whole, refusing one over maxBytes (never cutting it).
func ReadArchiveBytes(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if err = checkArchiveSize(int64(len(body)), maxBytes); err != nil {
		return nil, err
	}
	return body, nil
}

// archiveBudget tracks the entries and bytes of one archive, read or written, against one set of limits.
type archiveBudget struct {
	lim     exerciseModel.ArchiveLimits
	total   int64
	entries int
}

func newArchiveBudget(lim exerciseModel.ArchiveLimits) *archiveBudget {
	return &archiveBudget{lim: lim}
}

// check refuses an entry the zip header already shows to be too large or expanding too far, before it is read.
func (b *archiveBudget) check(entries int, uncompressed, compressed uint64) error {
	if err := checkEntryCount(entries, b.lim); err != nil {
		return err
	}
	if uncompressed > uint64(b.lim.EntryBytes) {
		return archiveTooLarge("a file is over " + mib(b.lim.EntryBytes) + " unpacked")
	}
	if uncompressed > archiveRatioFreeBytes && (compressed == 0 || uncompressed/compressed > maxArchiveRatio) {
		return archiveTooLarge(fmt.Sprintf("a file expands more than %d times", maxArchiveRatio))
	}
	return nil
}

// read unpacks one entry within the limits; the header's sizes are not trusted, the bytes actually read count.
func (b *archiveBudget) read(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, b.lim.EntryBytes+1))
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
	if int64(size) > b.lim.EntryBytes {
		return archiveTooLarge("a file is over " + mib(b.lim.EntryBytes) + " unpacked")
	}
	if b.total += int64(size); b.total > b.lim.UnpackedBytes {
		return archiveTooLarge("the files are over " + mib(b.lim.UnpackedBytes) + " unpacked together")
	}
	return nil
}

// checkEntryCount refuses an archive of more than lim.Entries entries.
func checkEntryCount(count int, lim exerciseModel.ArchiveLimits) error {
	if count > lim.Entries {
		return archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", lim.Entries))
	}
	return nil
}

// checkExportEntries refuses an archive to be written with these files and a manifest.json beside them that the
// import would refuse.
func checkExportEntries(files map[string][]byte, lim exerciseModel.ArchiveLimits) error {
	if err := checkEntryCount(len(files)+1, lim); err != nil {
		return err
	}
	budget := newArchiveBudget(lim)
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
