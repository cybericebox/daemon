package exercise

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
	"golang.org/x/text/unicode/norm"
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

// archiveLitterFiles are the files the operating systems add to a folder on their own: .DS_Store (macOS), Thumbs.db and
// desktop.ini (Windows). They are ignored in any folder, whatever the case of the name.
var archiveLitterFiles = []string{".DS_Store", "Thumbs.db", "desktop.ini"}

// isArchiveLitter is what an operating system adds to a zip on its own: resource forks under __MACOSX/ (macOS)
// and the service files of archiveLitterFiles.
func isArchiveLitter(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	if first == "__MACOSX" {
		return true
	}
	base := path.Base(name)
	for _, litter := range archiveLitterFiles {
		if strings.EqualFold(base, litter) {
			return true
		}
	}
	return false
}

// checkArchiveEntry decides on one zip entry before it is read: a symlink or an unsafe path is refused, a
// directory entry is skipped (only files are kept). The root manifest.json is the one name safeArchivePath
// refuses that an archive may hold. The name is returned in NFC (macOS writes NFD): two entries that are the
// same name once normalized are refused, as is any repeated entry. A backslash in an entry name is a separator
// (some Windows tools write it that way): the name is read with it as a slash, and every path check, the
// drive letter and the duplicate check included, runs on that form.
func checkArchiveEntry(raw string, mode fs.FileMode, seen map[string][]byte) (name string, skip bool, err error) {
	if mode&fs.ModeSymlink != 0 {
		return "", false, archivePathInvalid(raw, "symlink")
	}
	name = norm.NFC.String(strings.ReplaceAll(raw, "\\", "/"))
	if dir, ok := strings.CutSuffix(name, "/"); ok && mode.IsDir() && safeArchivePath(dir) {
		return "", true, nil
	}
	if !safeArchivePath(name) && name != "manifest.json" {
		return "", false, archivePathInvalid(raw, "unsafe path")
	}
	if _, dup := seen[name]; dup {
		return "", false, archivePathInvalid(raw, "duplicate path")
	}
	return name, false, nil
}

// splitTaskFolders groups the archive entries by task folder: every directory whose direct children include
// exercise.json or exercise.yaml, at any depth, the zip root included. A task folder inside another and an
// entry outside every task folder are errors; macOS litter is dropped.
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
	// No root manifest.json reaches here: validateArchiveManifest checked and removed it while reading.
	for _, name := range names {
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
	// images are the folder's device image files (relative paths), read aside; nil when not imported.
	images map[string]*archiveImageFile
	// imageMarkers is the marker given to each image file already linked.
	imageMarkers map[string]string
}

func newArchiveLinks(files map[string][]byte, parsed *ParsedExerciseArchive) *archiveLinks {
	parsed.FileMeta = map[uuid.UUID]exportedFile{}
	parsed.FilePaths = map[uuid.UUID]string{}
	return &archiveLinks{files: files, byHash: map[[sha256.Size]byte]uuid.UUID{}, linked: map[string]bool{}, parsed: parsed}
}

// resolve checks one attachment link (a path relative to the task folder) and returns the archive path it names.
func (l *archiveLinks) resolve(file string) (string, error) {
	clean, err := checkLinkPath(file)
	if err != nil {
		return "", err
	}
	if _, isImage := l.images[clean]; isImage {
		return "", archivePathInvalid(file, "a device image file, not an attachment")
	}
	if _, ok := l.files[clean]; !ok {
		return "", archiveFileMissing(file)
	}
	l.linked[clean] = true
	return clean, nil
}

// checkLinkPath cleans a link and refuses one that is empty, absolute, leaves the task folder, names a reserved
// file or is otherwise unsafe.
func checkLinkPath(file string) (string, error) {
	clean := path.Clean(norm.NFC.String(file))
	switch {
	case file == "":
		return "", archivePathInvalid(file, "empty path")
	case strings.Contains(file, "\\"):
		return "", archivePathInvalid(file, "backslash")
	case strings.HasPrefix(file, "/"):
		return "", archivePathInvalid(file, "absolute path")
	}
	switch {
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "", archivePathInvalid(file, "leaves the task folder")
	case clean == "exercise.json" || clean == "exercise.yaml" || clean == "manifest.json":
		return "", archivePathInvalid(file, "reserved file")
	case !safeArchivePath(clean):
		return "", archivePathInvalid(file, "unsafe path")
	}
	return clean, nil
}

// image links a device image file; the same file named by several devices is one image (pushed once, under the
// first device's name). It returns the marker the device holds until the image is pushed.
func (l *archiveLinks) image(device, file string) (string, error) {
	clean, err := checkLinkPath(file)
	if err != nil {
		return "", err
	}
	entry, ok := l.images[clean]
	if !ok {
		return "", archiveFileMissing(file)
	}
	l.linked[clean] = true
	if marker, done := l.imageMarkers[clean]; done {
		return marker, nil
	}
	if l.imageMarkers == nil {
		l.imageMarkers = map[string]string{}
	}
	marker := imageFileMarker + entry.Name
	l.imageMarkers[clean] = marker
	l.parsed.Images = append(l.parsed.Images, ParsedArchiveImage{Path: clean, Device: device, Marker: marker, file: entry})
	return marker, nil
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
	if strings.ContainsAny(name, "/\\") || strings.ContainsFunc(name, unicode.IsControl) {
		return exerciseModel.AttachmentRef{}, archiveInvalid(fmt.Sprintf("%s.name must not contain /, \\ or control characters", where))
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
