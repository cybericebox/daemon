package ociregistry

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// headersOnly is a tar of n empty files named by name(i), written with fixed times (PAX where a name needs it).
func headersOnly(t *testing.T, n int, name func(int) string) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for i := 0; i < n; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: name(i), Mode: 0o644, Typeflag: tar.TypeReg, ModTime: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC), Format: tar.FormatPAX}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func withIndexLimits(t *testing.T, entries int, nameBytes int64) {
	t.Helper()
	oldEntries, oldBytes := maxIndexEntries, maxIndexNameBytes
	maxIndexEntries, maxIndexNameBytes = entries, nameBytes
	t.Cleanup(func() { maxIndexEntries, maxIndexNameBytes = oldEntries, oldBytes })
}

func readAll(b []byte) error {
	_, err := ReadImageArchive(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }, int64(len(b)), 10<<30)
	return err
}

func tooLarge(t *testing.T, err error) {
	t.Helper()
	var ae *ArchiveError
	if !errors.As(err, &ae) || ae.Code != ArchiveTooLarge {
		t.Fatalf("err %v, want too_large", err)
	}
}

// The index of an archive of many tiny headers is bounded: past the entry cap it is too large.
func TestIndexCapsTheEntryCount(t *testing.T) {
	if maxIndexEntries != 100_000 || maxIndexNameBytes != 64<<20 {
		t.Fatalf("limits %d entries, %d name bytes", maxIndexEntries, maxIndexNameBytes)
	}
	withIndexLimits(t, 50, 64<<20)
	tooLarge(t, readAll(headersOnly(t, 51, func(i int) string { return fmt.Sprintf("f%05d", i) })))
	// At the cap the archive is read (and found to hold no image).
	var ae *ArchiveError
	if err := readAll(headersOnly(t, 50, func(i int) string { return fmt.Sprintf("f%05d", i) })); !errors.As(err, &ae) || ae.Code != ArchiveNoImage {
		t.Fatalf("at the cap: %v", err)
	}
}

// Long PAX names count towards the kept name bytes.
func TestIndexCapsTheNameBytes(t *testing.T) {
	withIndexLimits(t, 100_000, 10_000)
	tooLarge(t, readAll(headersOnly(t, 3, func(i int) string { return fmt.Sprintf("%d/%s", i, strings.Repeat("n", 4_000)) })))
}
