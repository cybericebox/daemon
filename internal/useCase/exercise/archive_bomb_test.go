package exercise

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
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

// The reported PoC: a few MB of zip that unpack into gigabytes of memory.
func TestAnArchiveThatExpandsFarBeyondItsSizeIsRefused(t *testing.T) {
	bomb := zipOf(t, map[string][]byte{"a.bin": make([]byte, 48<<20)})
	if len(bomb) > 1<<20 {
		t.Fatalf("the test archive should be tiny, it is %d bytes", len(bomb))
	}
	if _, err := ReadArchiveV1(bytes.NewReader(bomb)); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a 48 MiB entry in a ~50 KB archive must be refused: %v", err)
	}
	// An entry over the entry cap is refused whatever its ratio.
	if _, err := ReadArchiveV1(bytes.NewReader(zipOf(t, map[string][]byte{"a.bin": make([]byte, 65<<20)}))); err == nil {
		t.Fatal("an entry over 64 MiB must be refused")
	}
}

func TestAnArchiveWithTooManyEntriesIsRefused(t *testing.T) {
	entries := map[string][]byte{}
	for i := 0; i <= maxArchiveEntries; i++ {
		entries[fmt.Sprintf("f%04d.txt", i)] = []byte("x")
	}
	if _, err := ReadArchiveV1(bytes.NewReader(zipOf(t, entries))); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheUnpackedTotalIsCappedAndTheBytesActuallyReadCount(t *testing.T) {
	var b archiveBudget
	chunk := bytes.Repeat([]byte{1}, 60<<20)
	for i := 0; i < 4; i++ { // 4 x 60 MiB = 240 MiB: within the total
		if _, err := b.read(bytes.NewReader(chunk)); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	if _, err := b.read(bytes.NewReader(chunk)); err == nil {
		t.Fatal("the fifth chunk passes 256 MiB in total")
	}
	// The header can lie: a stream longer than the entry cap is refused, never silently cut.
	var other archiveBudget
	if _, err := other.read(bytes.NewReader(make([]byte, 65<<20))); err == nil {
		t.Fatal("an entry longer than 64 MiB must be an error, not a truncated body")
	}
}

func TestAnOrdinaryArchiveStillReads(t *testing.T) {
	files, err := BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"x":1}`), "files/a.txt": bytes.Repeat([]byte("hello "), 1000)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadArchiveV1(bytes.NewReader(files))
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err %v", len(got), err)
	}
}
