package exercise

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	yzip "github.com/yeka/zip"
)

func rezipWithOldManifest(t *testing.T, original []byte, edited string, body []byte) []byte {
	t.Helper()
	files, err := ReadArchiveV1(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	manifestSource, err := BuildArchiveV1(files)
	if err != nil {
		t.Fatal(err)
	}
	manifest := unzipRaw(t, manifestSource)["manifest.json"]
	raw := unzipRaw(t, original)
	raw[edited] = body
	raw["manifest.json"] = manifest
	return zipOf(t, raw)
}

func unzipRaw(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	// ReadArchiveV1 strips the manifest, so read the entries through the zip directly.
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err = buf.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
		out[f.Name] = buf.Bytes()
	}
	return out
}

func TestReadArchiveV1_WithoutManifest(t *testing.T) {
	data, err := BuildArchiveNoManifest(map[string][]byte{"exercise.json": []byte(minimalDoc), "files/a.txt": []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unzipRaw(t, data)["manifest.json"]; ok {
		t.Fatal("BuildArchiveNoManifest must not write manifest.json")
	}
	files, err := ReadArchiveV1(bytes.NewReader(data))
	if err != nil || len(files) != 2 {
		t.Fatalf("files %d err %v", len(files), err)
	}
}

func TestReadArchiveV1_ChecksumMismatchHint(t *testing.T) {
	original, err := BuildArchiveV1(map[string][]byte{"exercise.json": []byte(minimalDoc), "files/a.txt": []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	edited := rezipWithOldManifest(t, original, "files/a.txt", []byte("changed"))
	_, err = ReadArchiveV1(bytes.NewReader(edited))
	if !errors.Is(err, exerciseModel.ErrArchiveChecksumMismatch.Err()) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadArchiveV1_FolderManifestCoversItsFolder(t *testing.T) {
	inner := map[string][]byte{"exercise.json": []byte(minimalDoc), "files/a.txt": []byte("a")}
	folder, err := BuildArchiveV1(inner)
	if err != nil {
		t.Fatal(err)
	}
	raw := unzipRaw(t, folder)
	nested := map[string][]byte{}
	for name, body := range raw {
		nested["task/"+name] = body
	}
	files, err := ReadArchiveV1(bytes.NewReader(zipOf(t, nested)))
	if err != nil || len(files) != 2 {
		t.Fatalf("files %d err %v", len(files), err)
	}
	nested["task/files/a.txt"] = []byte("changed")
	if _, err = ReadArchiveV1(bytes.NewReader(zipOf(t, nested))); !errors.Is(err, exerciseModel.ErrArchiveChecksumMismatch.Err()) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseExerciseArchive_MismatchWithPassword(t *testing.T) {
	original, err := BuildArchiveV1(map[string][]byte{"exercise.json": []byte(minimalDoc), "files/a.txt": []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	edited := rezipWithOldManifest(t, original, "files/a.txt", []byte("changed"))
	_, err = ParseExerciseArchive(edited, "x")
	if !errors.Is(err, exerciseModel.ErrArchiveChecksumMismatch.Err()) {
		t.Fatalf("err = %v, want the mismatch error, not a password hint", err)
	}
}

func TestReadProtectedArchive_WithoutManifest(t *testing.T) {
	var out bytes.Buffer
	zw := yzip.NewWriter(&out)
	w, err := zw.Encrypt("exercise.json", "pw", yzip.AES256Encryption)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte(minimalDoc)); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := ReadProtectedArchiveV1(out.Bytes(), "pw")
	if err != nil || len(files) != 1 {
		t.Fatalf("files %d err %v", len(files), err)
	}
}

func TestReadArchiveV1_LimitsStillApply(t *testing.T) {
	if _, err := ReadArchiveV1(bytes.NewReader(zipOf(t, map[string][]byte{"../x": []byte("x")}))); !errors.Is(err, exerciseModel.ErrArchivePathInvalid.Err()) {
		t.Fatalf("an unsafe path must be refused without a manifest: %v", err)
	}
	entries := map[string][]byte{}
	for i := 0; i <= maxArchiveEntries; i++ {
		entries[fmt.Sprintf("f%04d.txt", i)] = []byte("x")
	}
	if _, err := ReadArchiveV1(bytes.NewReader(zipOf(t, entries))); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("1001 entries must be refused without a manifest: %v", err)
	}
}

func TestReadArchiveV1_NestedManifestNamedAttachmentSurvives(t *testing.T) {
	extension := []byte(`{"manifest_version": 3}`)
	for name, build := range map[string]func(map[string][]byte) ([]byte, error){"with root manifest": BuildArchiveV1, "without manifest": BuildArchiveNoManifest} {
		data, err := build(map[string][]byte{"task/exercise.json": []byte(minimalDoc), "task/files/manifest.json": extension})
		if err != nil {
			t.Fatal(err)
		}
		files, err := ReadArchiveV1(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(files["task/files/manifest.json"], extension) || len(files) != 2 {
			t.Fatalf("%s: the attachment must stay untouched, got %d entries", name, len(files))
		}
	}
}

func TestReadArchiveV1_ManifestListingAMissingFileIsAMismatch(t *testing.T) {
	manifest := []byte(`{"version":1,"checksums":{"gone.txt":"` + hashBytes([]byte("x")) + `"}}`)
	data := zipOf(t, map[string][]byte{"a.txt": []byte("a"), "manifest.json": manifest})
	if _, err := ReadArchiveV1(bytes.NewReader(data)); !errors.Is(err, exerciseModel.ErrArchiveChecksumMismatch.Err()) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadArchiveV1_EntryNotListedInManifestIsAcceptedUnverified(t *testing.T) {
	manifest := []byte(`{"version":1,"checksums":{"a.txt":"` + hashBytes([]byte("a")) + `"}}`)
	data := zipOf(t, map[string][]byte{"a.txt": []byte("a"), "extra.txt": []byte("b"), "manifest.json": manifest})
	files, err := ReadArchiveV1(bytes.NewReader(data))
	if err != nil || len(files) != 2 || string(files["extra.txt"]) != "b" {
		t.Fatalf("files %d err %v", len(files), err)
	}
}
