package exercise

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckArchiveEntry_BackslashIsASeparator(t *testing.T) {
	name, skip, err := checkArchiveEntry(`task\files\a.txt`, 0, map[string][]byte{})
	require.NoError(t, err)
	require.False(t, skip)
	require.Equal(t, "task/files/a.txt", name)

	// a directory entry written the Windows way is a directory
	_, skip, err = checkArchiveEntry(`task\files\`, fs.ModeDir, map[string][]byte{})
	require.NoError(t, err)
	require.True(t, skip)
}

func TestCheckArchiveEntry_WindowsPathsStayChecked(t *testing.T) {
	for _, raw := range []string{`..\x`, `a\..\..\x`, `a\..\x`, `C:\x`, `c:/x`, `\x`, `/x`, `a\\b`, `a\.\b`, `.\x`} {
		_, _, err := checkArchiveEntry(raw, 0, map[string][]byte{})
		require.Errorf(t, err, "%q must be refused", raw)
	}
}

func TestCheckArchiveEntry_BackslashAndSlashNamesAreDuplicates(t *testing.T) {
	seen := map[string][]byte{"a/b.txt": []byte("x")}
	_, _, err := checkArchiveEntry(`a\b.txt`, 0, seen)
	require.Error(t, err)
}

func TestIsArchiveLitter_WindowsFiles(t *testing.T) {
	for _, name := range []string{".DS_Store", "a/.DS_Store", "__MACOSX/a/._x", "Thumbs.db", "a/b/Thumbs.db", "thumbs.DB", "desktop.ini", "a/Desktop.INI"} {
		require.Truef(t, isArchiveLitter(name), "%q is litter", name)
	}
	for _, name := range []string{"thumbs.db.txt", "a/desktop.ini/x", "my-thumbs.db", "files/Thumbs.db.png"} {
		require.Falsef(t, isArchiveLitter(name), "%q is a real file", name)
	}
}

// An archive written by a Windows tool (backslashes in the entry names, Thumbs.db and desktop.ini inside) imports.
func TestParseExerciseArchive_WindowsArchive(t *testing.T) {
	doc, err := exampleFS.ReadFile("example/exercise.yaml")
	require.NoError(t, err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{
		`my-task\exercise.yaml`:     doc,
		`my-task\files\notes.txt`:   []byte("a"),
		`my-task\files\notes-2.txt`: []byte("b"),
		`my-task\Thumbs.db`:         []byte("x"),
		`my-task\files\desktop.ini`: []byte("x"),
	} {
		w, createErr := zw.Create(name)
		require.NoError(t, createErr)
		_, writeErr := w.Write(body)
		require.NoError(t, writeErr)
	}
	require.NoError(t, zw.Close())
	imports, err := ExpandExerciseArchiveBundle(buf.Bytes(), "", DefaultLimits())
	require.NoError(t, err)
	require.Len(t, imports, 1)
	parsed, err := ParseExerciseArchive(imports[0].Archive, "", DefaultLimits())
	require.NoError(t, err)
	require.Equal(t, "Приклад завдання", parsed.Exercise.Name)
}
