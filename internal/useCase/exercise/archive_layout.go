package exercise

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

// maxAttachmentNameRunes caps the shown name of an attachment.
const maxAttachmentNameRunes = 255

// taskFolder is one task inside an archive: Prefix is its directory ("" for the zip root), Files its
// entries with paths relative to it.
type taskFolder struct {
	Prefix string
	Files  map[string][]byte
}

// archivePathInvalid is the one place ErrArchivePathInvalid is raised; file is the path as written.
func archivePathInvalid(file, reason string) error {
	return exerciseModel.ErrArchivePathInvalid.WithPublicContext("file", file).WithPublicContext("reason", reason).Err()
}

// archiveFileUnlinked is the one place ErrArchiveFileUnlinked is raised.
func archiveFileUnlinked(file string) error {
	return exerciseModel.ErrArchiveFileUnlinked.WithPublicContext("file", file).Err()
}

// archiveFileMissing is the one place ErrArchiveFileMissing is raised; file is the link as written.
func archiveFileMissing(file string) error {
	return exerciseModel.ErrArchiveFileMissing.WithPublicContext("file", file).Err()
}

// isArchiveLitter is what macOS adds to a zip on its own: resource forks under __MACOSX/ and .DS_Store.
func isArchiveLitter(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	return first == "__MACOSX" || path.Base(name) == ".DS_Store"
}

// checkArchiveEntry decides on one zip entry before it is read: a symlink or an unsafe path is refused, a
// directory entry is skipped (only files are kept). The root manifest.json is the one name safeArchivePath
// refuses that an archive may hold.
func checkArchiveEntry(name string, mode fs.FileMode) (skip bool, err error) {
	if mode&fs.ModeSymlink != 0 {
		return false, archivePathInvalid(name, "symlink")
	}
	if dir, ok := strings.CutSuffix(name, "/"); ok && mode.IsDir() && safeArchivePath(dir) {
		return true, nil
	}
	if !safeArchivePath(name) && name != "manifest.json" {
		return false, archivePathInvalid(name, "unsafe path")
	}
	return false, nil
}

// splitTaskFolders groups the archive entries by task folder: every directory whose direct children include
// exercise.json or exercise.yaml, at any depth, the zip root included. A task folder inside another and an
// entry outside every task folder (other than the root manifest.json) are errors; macOS litter is dropped.
func splitTaskFolders(files map[string][]byte) ([]taskFolder, error) {
	kept := make(map[string][]byte, len(files))
	prefixes := map[string]bool{}
	for name, body := range files {
		if isArchiveLitter(name) {
			continue
		}
		kept[name] = body
		if base := path.Base(name); base == "exercise.json" || base == "exercise.yaml" {
			dir := path.Dir(name)
			if dir == "." {
				dir = ""
			}
			prefixes[dir] = true
		}
	}
	if len(prefixes) == 0 {
		return nil, archiveInvalid("missing exercise.json or exercise.yaml")
	}
	sorted := make([]string, 0, len(prefixes))
	for prefix := range prefixes {
		sorted = append(sorted, prefix)
	}
	sort.Strings(sorted)
	for _, inner := range sorted {
		for _, outer := range sorted {
			if inner != outer && (outer == "" || strings.HasPrefix(inner, outer+"/")) {
				return nil, archiveInvalid("nested task folder " + inner)
			}
		}
	}
	folders := make([]taskFolder, len(sorted))
	for i, prefix := range sorted {
		folders[i] = taskFolder{Prefix: prefix, Files: map[string][]byte{}}
	}
	names := sortedArchiveNames(kept)
	for _, name := range names {
		if name == "manifest.json" {
			continue // the root manifest covers the whole archive, no task folder holds it
		}
		placed := false
		for i, prefix := range sorted {
			if prefix == "" {
				folders[i].Files[name], placed = kept[name], true
				break
			}
			if rel, ok := strings.CutPrefix(name, prefix+"/"); ok {
				folders[i].Files[rel], placed = kept[name], true
				break
			}
		}
		if !placed {
			return nil, archiveFileUnlinked(name)
		}
	}
	return folders, nil
}

// archiveLinks resolves the file links of one task folder's document: each distinct content (SHA-256) gets
// one fresh file id, and every linked path is remembered for the orphan check.
type archiveLinks struct {
	files  map[string][]byte
	byHash map[[sha256.Size]byte]uuid.UUID
	linked map[string]bool
	parsed *ParsedExerciseArchive
}

func newArchiveLinks(files map[string][]byte, parsed *ParsedExerciseArchive) *archiveLinks {
	parsed.FileMeta = map[uuid.UUID]exportedFile{}
	parsed.FilePaths = map[uuid.UUID]string{}
	return &archiveLinks{files: files, byHash: map[[sha256.Size]byte]uuid.UUID{}, linked: map[string]bool{}, parsed: parsed}
}

// resolve checks one link (a path relative to the task folder) and returns the archive path it names.
func (l *archiveLinks) resolve(file string) (string, error) {
	switch {
	case file == "":
		return "", archivePathInvalid(file, "empty path")
	case strings.Contains(file, "\\"):
		return "", archivePathInvalid(file, "backslash")
	case strings.HasPrefix(file, "/"):
		return "", archivePathInvalid(file, "absolute path")
	}
	clean := path.Clean(file)
	switch {
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "", archivePathInvalid(file, "leaves the task folder")
	case clean == "exercise.json" || clean == "exercise.yaml" || clean == "manifest.json":
		return "", archivePathInvalid(file, "reserved file")
	case !safeArchivePath(clean):
		return "", archivePathInvalid(file, "unsafe path")
	}
	if _, ok := l.files[clean]; !ok {
		return "", archiveFileMissing(file)
	}
	l.linked[clean] = true
	return clean, nil
}

// attachment maps one attachment link; where is its 1-based document path.
func (l *archiveLinks) attachment(where string, a archiveAttachmentV2) (exerciseModel.AttachmentRef, error) {
	clean, err := l.resolve(a.File)
	if err != nil {
		return exerciseModel.AttachmentRef{}, err
	}
	name := a.Name
	if name == "" {
		name = path.Base(clean)
	}
	if utf8.RuneCountInString(name) > maxAttachmentNameRunes {
		return exerciseModel.AttachmentRef{}, archiveInvalid(fmt.Sprintf("%s.name is longer than %d characters", where, maxAttachmentNameRunes))
	}
	body := l.files[clean]
	hash := sha256.Sum256(body)
	id, seen := l.byHash[hash]
	if !seen {
		id = freshID()
		l.byHash[hash] = id
		l.parsed.FileMeta[id] = exportedFile{Name: name, ContentType: mime.TypeByExtension(path.Ext(name))}
		l.parsed.FilePaths[id] = clean
	}
	return exerciseModel.AttachmentRef{FileID: id, Name: name}, nil
}

// orphan reports the first (alphabetical) folder file the document does not link to.
func (l *archiveLinks) orphan() error {
	for _, name := range sortedArchiveNames(l.files) {
		if name == "exercise.json" || name == "exercise.yaml" || l.linked[name] {
			continue
		}
		return archiveFileUnlinked(name)
	}
	return nil
}

// lookupAttachment returns the bytes of an imported file by the id toParsed gave it.
func lookupAttachment(parsed *ParsedExerciseArchive, id uuid.UUID) ([]byte, bool) {
	name, ok := parsed.FilePaths[id]
	if !ok {
		return nil, false
	}
	body, ok := parsed.Files[name]
	return body, ok
}
