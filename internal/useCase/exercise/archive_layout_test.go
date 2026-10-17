package exercise

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

const layoutDoc = `schema: cib-exercise/1
exercise:
  name: Layout
versions:
  - status: draft
    variants:
      - tasks:
          - name: t
`

func folderPrefixes(folders []taskFolder) []string {
	out := make([]string, 0, len(folders))
	for _, folder := range folders {
		out = append(out, folder.Prefix)
	}
	return out
}

func TestSplitTaskFolders_RootTask(t *testing.T) {
	folders, err := splitTaskFolders(map[string][]byte{"exercise.yaml": []byte("y"), "files/a.txt": []byte("a")})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(folders) != 1 || folders[0].Prefix != "" {
		t.Fatalf("want one root folder, got %v", folderPrefixes(folders))
	}
	if string(folders[0].Files["files/a.txt"]) != "a" || folders[0].Files["exercise.yaml"] == nil {
		t.Fatalf("root folder files: %v", folders[0].Files)
	}
}

func TestSplitTaskFolders_SeveralAtAnyDepth(t *testing.T) {
	folders, err := splitTaskFolders(map[string][]byte{
		"web/t1/exercise.yaml": []byte("y"),
		"web/t1/x/y.bin":       []byte("b"),
		"pwn/exercise.json":    []byte("j"),
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if got := strings.Join(folderPrefixes(folders), ","); got != "pwn,web/t1" {
		t.Fatalf("prefixes = %s", got)
	}
	if len(folders[1].Files) != 2 || string(folders[1].Files["x/y.bin"]) != "b" {
		t.Fatalf("web/t1 files are not relative: %v", folders[1].Files)
	}
	if len(folders[0].Files) != 1 || folders[0].Files["exercise.json"] == nil {
		t.Fatalf("pwn files: %v", folders[0].Files)
	}
}

func TestSplitTaskFolders_NestedTaskRejected(t *testing.T) {
	_, err := splitTaskFolders(map[string][]byte{"a/exercise.yaml": []byte("y"), "a/b/exercise.json": []byte("j")})
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("want ErrArchiveInvalid, got %v", err)
	}
	if reason := publicContext(t, err)["reason"]; reason != "nested task folder a/b" {
		t.Fatalf("reason = %v", reason)
	}
	// The zip root is a task folder too.
	if _, err = splitTaskFolders(map[string][]byte{"exercise.yaml": []byte("y"), "b/exercise.json": []byte("j")}); !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("root + nested: want ErrArchiveInvalid, got %v", err)
	}
}

func TestSplitTaskFolders_StrayFileRejected(t *testing.T) {
	_, err := splitTaskFolders(map[string][]byte{
		"a/exercise.yaml": []byte("y"), "b/exercise.json": []byte("j"), "readme.md": []byte("r"),
	})
	if !errors.Is(err, exerciseModel.ErrArchiveFileUnlinked.Err()) {
		t.Fatalf("want ErrArchiveFileUnlinked, got %v", err)
	}
	if file := publicContext(t, err)["file"]; file != "readme.md" {
		t.Fatalf("file = %v", file)
	}
	folders, err := splitTaskFolders(map[string][]byte{
		"a/exercise.yaml": []byte("y"), "b/exercise.json": []byte("j"), "manifest.json": []byte("{}"),
		"__MACOSX/a/._x": []byte("m"), "a/.DS_Store": []byte("d"), ".DS_Store": []byte("d"),
	})
	if err != nil {
		t.Fatalf("manifest and macOS litter must be accepted: %v", err)
	}
	if len(folders) != 2 || len(folders[0].Files) != 1 {
		t.Fatalf("litter must be dropped: %+v", folders)
	}
}

func TestSplitTaskFolders_NoTaskFolder(t *testing.T) {
	_, err := splitTaskFolders(map[string][]byte{"docs/a.txt": []byte("a")})
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) || publicContext(t, err)["reason"] != "missing exercise.json or exercise.yaml" {
		t.Fatalf("want missing document, got %v", err)
	}
}

func TestSplitTaskFolders_BothDocumentsInOneFolder(t *testing.T) {
	folders, err := splitTaskFolders(map[string][]byte{"a/exercise.yaml": []byte("y"), "a/exercise.json": []byte("j")})
	if err != nil || len(folders) != 1 {
		t.Fatalf("split: %v %v", folders, err)
	}
	if _, _, err = pickExerciseDocument(folders[0].Files); !errors.Is(err, exerciseModel.ErrArchiveBothDocuments.Err()) {
		t.Fatalf("want ErrArchiveBothDocuments, got %v", err)
	}
}

func TestReadArchive_SymlinkEntryRejected(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, mode := range map[string]fs.FileMode{"exercise.yaml": 0o644, "files/link": fs.ModeSymlink | 0o777} {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(mode)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte("/etc/passwd")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := ReadArchiveV1(bytes.NewReader(buf.Bytes()))
	if !errors.Is(err, exerciseModel.ErrArchivePathInvalid.Err()) {
		t.Fatalf("want ErrArchivePathInvalid, got %v", err)
	}
	if ctx := publicContext(t, err); ctx["reason"] != "symlink" || ctx["file"] != "files/link" {
		t.Fatalf("context = %v", ctx)
	}
}

func TestReadArchive_DirectoryEntriesSkipped(t *testing.T) {
	data := zipOf(t, map[string][]byte{"task/": nil, "task/exercise.yaml": []byte(layoutDoc)})
	files, err := ReadArchiveV1(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(files) != 1 || files["task/exercise.yaml"] == nil {
		t.Fatalf("files = %v", files)
	}
}

// taskWithAttachments is a YAML document whose only task links the given attachment lines.
func taskWithAttachments(lines ...string) []byte {
	doc := layoutDoc + "            attachments:\n"
	for _, line := range lines {
		doc += "              - " + line + "\n"
	}
	return []byte(doc)
}

func parseLayout(t *testing.T, files map[string][]byte) (ParsedExerciseArchive, error) {
	t.Helper()
	data, err := BuildArchiveNoManifest(files)
	if err != nil {
		t.Fatal(err)
	}
	return ParseExerciseArchive(data, "")
}

func TestToParsed_AttachmentByPathAnyFolder(t *testing.T) {
	parsed, err := parseLayout(t, map[string][]byte{
		"exercise.yaml":          taskWithAttachments(`{file: docs/notes/Нотатки.txt}`),
		"docs/notes/Нотатки.txt": []byte("текст"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ref := parsed.Versions[0].Variants[0].Tasks[0].Attachments[0]
	if ref.Name != "Нотатки.txt" || ref.FileID == uuid.Nil {
		t.Fatalf("ref = %+v", ref)
	}
	meta := parsed.FileMeta[ref.FileID]
	if meta.Name != "Нотатки.txt" || !strings.HasPrefix(meta.ContentType, "text/plain") {
		t.Fatalf("meta = %+v", meta)
	}
	body, ok := lookupAttachment(&parsed, ref.FileID)
	if !ok || string(body) != "текст" {
		t.Fatalf("lookup = %q %v", body, ok)
	}
}

func TestToParsed_AttachmentShownName(t *testing.T) {
	parsed, err := parseLayout(t, map[string][]byte{
		"exercise.yaml": taskWithAttachments(`{file: files/./a.bin, name: dump.pcap}`),
		"files/a.bin":   []byte("a"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ref := parsed.Versions[0].Variants[0].Tasks[0].Attachments[0]
	if ref.Name != "dump.pcap" || parsed.FilePaths[ref.FileID] != "files/a.bin" {
		t.Fatalf("ref = %+v, path %q", ref, parsed.FilePaths[ref.FileID])
	}
	_, err = parseLayout(t, map[string][]byte{
		"exercise.yaml": taskWithAttachments(`{file: files/a.bin, name: ` + strings.Repeat("я", 256) + `}`),
		"files/a.bin":   []byte("a"),
	})
	if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		t.Fatalf("256-rune name: want ErrArchiveInvalid, got %v", err)
	}
}

func TestToParsed_AttachmentPathRejected(t *testing.T) {
	cases := []struct {
		file string
		want error
	}{
		{"../x", exerciseModel.ErrArchivePathInvalid.Err()},
		{"/etc/passwd", exerciseModel.ErrArchivePathInvalid.Err()},
		{`'files\x'`, exerciseModel.ErrArchivePathInvalid.Err()},
		{"files/../../x", exerciseModel.ErrArchivePathInvalid.Err()},
		{"manifest.json", exerciseModel.ErrArchivePathInvalid.Err()},
		{"exercise.yaml", exerciseModel.ErrArchivePathInvalid.Err()},
		{"''", exerciseModel.ErrArchivePathInvalid.Err()},
		{"files/none.txt", exerciseModel.ErrArchiveFileMissing.Err()},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			_, err := parseLayout(t, map[string][]byte{
				"exercise.yaml": taskWithAttachments(`{file: ` + c.file + `}`),
			})
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if _, ok := publicContext(t, err)["file"]; !ok {
				t.Fatalf("context names no file: %v", publicContext(t, err))
			}
		})
	}
}

func TestToParsed_UnlinkedFileRejected(t *testing.T) {
	_, err := parseLayout(t, map[string][]byte{
		"exercise.yaml":   taskWithAttachments(`{file: files/a.txt}`),
		"files/a.txt":     []byte("a"),
		"files/extra.txt": []byte("e"),
	})
	if !errors.Is(err, exerciseModel.ErrArchiveFileUnlinked.Err()) {
		t.Fatalf("want ErrArchiveFileUnlinked, got %v", err)
	}
	if file := publicContext(t, err)["file"]; file != "files/extra.txt" {
		t.Fatalf("file = %v", file)
	}
}

func TestToParsed_MacOSLitterIgnoredAtRoot(t *testing.T) {
	_, err := parseLayout(t, map[string][]byte{
		"exercise.yaml":            []byte(layoutDoc),
		"__MACOSX/._exercise.yaml": []byte("m"),
		".DS_Store":                []byte("d"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestToParsed_SameBytesOneFile(t *testing.T) {
	parsed, err := parseLayout(t, map[string][]byte{
		"exercise.yaml": taskWithAttachments(`{file: a/one.txt}`, `{file: b/two.txt}`, `{file: a/one.txt, name: again.txt}`, `{file: c.txt}`),
		"a/one.txt":     []byte("same"),
		"b/two.txt":     []byte("same"),
		"c.txt":         []byte("other"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	refs := parsed.Versions[0].Variants[0].Tasks[0].Attachments
	if refs[0].FileID != refs[1].FileID || refs[0].FileID != refs[2].FileID {
		t.Fatalf("equal bytes must share one id: %+v", refs)
	}
	if refs[3].FileID == refs[0].FileID {
		t.Fatalf("different bytes must get their own id: %+v", refs)
	}
	if refs[1].Name != "two.txt" || refs[2].Name != "again.txt" {
		t.Fatalf("shown names stay per link: %+v", refs)
	}
	if len(parsed.FileMeta) != 2 || len(parsed.FilePaths) != 2 || parsed.FilePaths[refs[0].FileID] != "a/one.txt" {
		t.Fatalf("meta %v paths %v", parsed.FileMeta, parsed.FilePaths)
	}
}

func TestExpandExerciseArchiveBundle_SplitsAnyDepth(t *testing.T) {
	bundle, err := BuildArchiveNoManifest(map[string][]byte{
		"web/t1/exercise.yaml": taskWithAttachments(`{file: x/y.txt}`),
		"web/t1/x/y.txt":       []byte("y"),
		"pwn/exercise.yaml":    []byte(layoutDoc),
		"__MACOSX/pwn/._x":     []byte("m"),
	})
	if err != nil {
		t.Fatal(err)
	}
	imports, err := ExpandExerciseArchiveBundle(bundle, "")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(imports) != 2 {
		t.Fatalf("want 2 imports, got %d", len(imports))
	}
	for _, item := range imports {
		if _, parseErr := ParseExerciseArchive(item.Archive, item.Password); parseErr != nil {
			t.Fatalf("parse import: %v", parseErr)
		}
	}
	if _, err = ExpandExerciseArchiveBundle(mustBuild(t, map[string][]byte{"a/exercise.yaml": []byte(layoutDoc), "notes.txt": []byte("n")}), ""); !errors.Is(err, exerciseModel.ErrArchiveFileUnlinked.Err()) {
		t.Fatalf("stray file: want ErrArchiveFileUnlinked, got %v", err)
	}
}

func TestExpandExerciseArchiveBundle_ProtectedRepack(t *testing.T) {
	bundle, err := BuildProtectedArchiveV1(map[string][]byte{"a/exercise.yaml": []byte(layoutDoc), "b/exercise.yaml": []byte(layoutDoc)}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	imports, err := ExpandExerciseArchiveBundle(bundle, "pw")
	if err != nil || len(imports) != 2 {
		t.Fatalf("expand: %v %d", err, len(imports))
	}
	for _, item := range imports {
		if item.Password != "pw" {
			t.Fatalf("password not kept: %+v", item)
		}
		if _, readErr := ReadProtectedArchiveV1(item.Archive, "pw"); readErr != nil {
			t.Fatalf("repacked archive is not protected: %v", readErr)
		}
	}
}

func mustBuild(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	data, err := BuildArchiveNoManifest(files)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
