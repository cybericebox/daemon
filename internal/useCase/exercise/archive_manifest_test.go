package exercise

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
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
	if _, err := ReadArchiveV1(bytes.NewReader(zipOf(t, map[string][]byte{"../x": []byte("x")}))); err == nil {
		t.Fatal("an unsafe path must be refused without a manifest")
	}
	entries := map[string][]byte{}
	for i := 0; i <= maxArchiveEntries; i++ {
		entries[fmt.Sprintf("f%04d.txt", i)] = []byte("x")
	}
	if _, err := ReadArchiveV1(bytes.NewReader(zipOf(t, entries))); err == nil {
		t.Fatal("1001 entries must be refused without a manifest")
	}
}
