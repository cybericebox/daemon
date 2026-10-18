package exercise

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func zipOf(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// incompressible is n fixed bytes deflate cannot shrink (a SHA-256 chain).
func incompressible(n int) []byte {
	out := make([]byte, 0, n+sha256.Size)
	seed := sha256.Sum256([]byte("incompressible"))
	for len(out) < n {
		out = append(out, seed[:]...)
		seed = sha256.Sum256(seed[:])
	}
	return out[:n]
}

func requireTooLarge(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, exerciseModel.ErrArchiveTooLarge.Err()) {
		t.Fatalf("%s: want ErrArchiveTooLarge, got %v", what, err)
	}
}

// Import and export share one set of limits: the export's, a stored file of the default media cap.
func TestArchiveLimitsAreTheExportLimits(t *testing.T) {
	want := Limits(512<<20, 512<<20, 512<<20, 1000)
	if got := exerciseModel.DefaultArchiveLimits(); got != want {
		t.Fatalf("limits = %+v", got)
	}
}

// The reported PoC: a few MB of zip that unpack into gigabytes of memory.
func TestAnArchiveThatExpandsFarBeyondItsSizeIsRefused(t *testing.T) {
	bomb := zipOf(t, map[string][]byte{"a.bin": make([]byte, 48<<20)})
	if len(bomb) > 1<<20 {
		t.Fatalf("the test archive should be tiny, it is %d bytes", len(bomb))
	}
	_, err := ReadArchiveV1(bytes.NewReader(bomb), DefaultLimits())
	requireTooLarge(t, err, "a 48 MiB entry in a ~50 KB archive")
}

func TestAnEntryOverTheEntryCapIsRefused(t *testing.T) {
	// Incompressible bytes, so the ratio check does not answer first.
	_, err := ReadArchiveV1(bytes.NewReader(zipOf(t, map[string][]byte{"a.bin": incompressible(3 << 20)})), Limits(8<<20, 2<<20, 8<<20, 1000))
	requireTooLarge(t, err, "an entry over the entry cap")
}

func TestAnArchiveWithTooManyEntriesIsRefused(t *testing.T) {
	entries := map[string][]byte{}
	for i := 0; i <= exerciseModel.DefaultArchiveEntries; i++ {
		entries[fmt.Sprintf("f%04d.txt", i)] = []byte("x")
	}
	_, err := ReadArchiveV1(bytes.NewReader(zipOf(t, entries)), DefaultLimits())
	requireTooLarge(t, err, "1001 entries")
}

func TestAnArchiveOverTheArchiveCapIsRefused(t *testing.T) {
	lim := Limits(1<<10, 1<<20, 1<<20, 1000)
	_, err := ReadArchiveBytes(bytes.NewReader(make([]byte, 1<<10+1)), lim.ArchiveBytes)
	requireTooLarge(t, err, "an upload over the archive cap")
	body, err := ReadArchiveBytes(bytes.NewReader(make([]byte, 1<<10)), lim.ArchiveBytes)
	if err != nil || len(body) != 1<<10 {
		t.Fatalf("an upload at the cap reads whole: %d %v", len(body), err)
	}
	big := zipOf(t, map[string][]byte{"a.bin": incompressible(2 << 10)})
	_, err = ReadArchiveV1(bytes.NewReader(big), lim)
	requireTooLarge(t, err, "a zip over the archive cap is refused, never cut")
	_, err = ReadProtectedArchiveV1(big, "pw", lim)
	requireTooLarge(t, err, "a protected zip over the archive cap")
}

func TestTheUnpackedTotalIsCappedAndTheBytesActuallyReadCount(t *testing.T) {
	lim := Limits(8<<20, 1<<20, 4<<20, 1000)
	b := newArchiveBudget(lim)
	chunk := bytes.Repeat([]byte{1}, 1<<20)
	for i := 0; i < 4; i++ { // 4 x 1 MiB: within the total
		if _, err := b.read(bytes.NewReader(chunk)); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	_, err := b.read(bytes.NewReader(chunk))
	requireTooLarge(t, err, "the fifth chunk passes the total")
	// The header can lie: a stream longer than the entry cap is refused, never silently cut.
	other := newArchiveBudget(lim)
	_, err = other.read(bytes.NewReader(make([]byte, 1<<20+1)))
	requireTooLarge(t, err, "an entry longer than the entry cap")
}

func TestAnOrdinaryArchiveStillReads(t *testing.T) {
	files, err := BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"x":1}`), "files/a.txt": bytes.Repeat([]byte("hello "), 1000)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadArchiveV1(bytes.NewReader(files), DefaultLimits())
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err %v", len(got), err)
	}
}
