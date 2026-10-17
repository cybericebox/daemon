package exercise

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	appErr "github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	yzip "github.com/yeka/zip"
)

// Device images inside a task archive (`image: {file: images/web.tar}`). Such an archive is read where it lies
// (an io.ReaderAt), never whole: the documents first, which name the image files; every other entry is read into
// memory as before (same limits), while the image files stay in the zip and are streamed from it twice each (index,
// then push). Image files do not count towards the unpacked total; each is at most MaxImageArchiveBytes, and all of
// them together too. The import is all or nothing: every task is parsed and checked, every image indexed and pushed,
// and only then are the tasks created.

// imageFileMarker starts the image a device holds while its image file is not pushed yet; the rest is the file's
// archive path. A reference in the document may not start with it.
const imageFileMarker = "\x00archive-image:"

// archiveImageFile is one device image file of the archive, left in the zip.
type archiveImageFile struct {
	// Name is the entry's path in the archive.
	Name string
	// Size is its unpacked size (the zip header's; the reader stops at it).
	Size int64
	open func() (io.ReadCloser, error)
	// checksum is the hex sha256 manifest.json lists for it ("" when it is not listed).
	checksum string
}

// ParsedArchiveImage is a device image file a task's document names: Device is the first device naming it (the
// platform image name), Marker what the devices hold until the push.
type ParsedArchiveImage struct {
	Path   string
	Device string
	Marker string
	file   *archiveImageFile
}

// zipEntry is one entry of a zip opened over an io.ReaderAt, plain or protected.
type zipEntry struct {
	raw          string
	mode         fs.FileMode
	uncompressed uint64
	compressed   uint64
	encrypted    bool
	// open reads the entry; stream (for image files) checks a protected entry's authentication code at its end
	// instead of buffering the whole entry first.
	open func(stream bool) (io.ReadCloser, error)
}

// openZipAt opens the zip over r: plain, or AES-protected when its entries are encrypted and a password is given
// (protected tells which). An encrypted archive without a password fails like the plain read of it.
func openZipAt(r io.ReaderAt, size int64, password string) ([]zipEntry, bool, error) {
	zbr := newOpenBudgetReaderAt(r)
	zr, err := zip.NewReader(zbr, size)
	if refused := zbr.finish(); refused != nil {
		return nil, false, refused
	}
	if err != nil {
		return nil, false, err
	}
	if len(zr.File) > limits.Entries {
		return nil, false, archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", limits.Entries))
	}
	encrypted := false
	for _, f := range zr.File {
		if f.Flags&0x1 != 0 {
			encrypted = true
		}
	}
	if !encrypted {
		entries := make([]zipEntry, 0, len(zr.File))
		for _, f := range zr.File {
			entries = append(entries, zipEntry{
				raw: f.Name, mode: f.Mode(), uncompressed: f.UncompressedSize64, compressed: f.CompressedSize64,
				open: func(bool) (io.ReadCloser, error) { return f.Open() },
			})
		}
		return entries, false, nil
	}
	if password == "" {
		return nil, false, zip.ErrAlgorithm
	}
	ybr := newOpenBudgetReaderAt(r)
	yr, err := yzip.NewReader(ybr, size)
	if refused := ybr.finish(); refused != nil {
		return nil, false, refused
	}
	if err != nil {
		return nil, false, err
	}
	entries := make([]zipEntry, 0, len(yr.File))
	for _, f := range yr.File {
		f.SetPassword(password)
		entries = append(entries, zipEntry{
			raw: f.Name, mode: f.Mode(), uncompressed: f.UncompressedSize64, compressed: f.CompressedSize64, encrypted: f.IsEncrypted(),
			open: func(stream bool) (io.ReadCloser, error) {
				f.DeferAuth = stream
				return f.Open()
			},
		})
	}
	return entries, true, nil
}

// archiveRead is an archive read with its image files left in the zip.
type archiveRead struct {
	// files are every entry but the image files (NFC names), manifest.json checked and removed.
	files map[string][]byte
	// images are the image files by archive path.
	images    map[string]*archiveImageFile
	protected bool
}

func isDocumentEntry(name string) bool {
	base := path.Base(name)
	return (base == "exercise.json" || base == "exercise.yaml") && !isArchiveLitter(name)
}

// checkedEntries applies the entry rules to every entry (symlinks, unsafe or repeated paths, the entry count) and
// returns the files by NFC name in archive order.
func checkedEntries(entries []zipEntry, protected bool) ([]string, map[string]zipEntry, error) {
	if len(entries) > limits.Entries {
		return nil, nil, archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", limits.Entries))
	}
	seen := map[string][]byte{}
	byName := map[string]zipEntry{}
	var names []string
	for _, e := range entries {
		name, skip, err := checkArchiveEntry(e.raw, e.mode, seen)
		if err != nil {
			return nil, nil, err
		}
		if skip {
			continue
		}
		if protected && !e.encrypted {
			return nil, nil, errUnencryptedEntry
		}
		seen[name], byName[name] = nil, e
		names = append(names, name)
	}
	return names, byName, nil
}

// readEntry reads one non-image entry within the budget.
func readEntry(budget *archiveBudget, count int, e zipEntry) ([]byte, error) {
	if err := budget.check(count, e.uncompressed, e.compressed); err != nil {
		return nil, err
	}
	rc, err := e.open(false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return budget.read(rc)
}

// readDocuments reads the exercise.json / exercise.yaml entries.
func readDocuments(names []string, byName map[string]zipEntry, count int, budget *archiveBudget) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range names {
		if !isDocumentEntry(name) {
			continue
		}
		body, err := readEntry(budget, count, byName[name])
		if err != nil {
			return nil, err
		}
		files[name] = body
	}
	return files, nil
}

// documentImageFiles is the set of archive paths the documents name as device image files, and whether any device
// names one at all. A link that is not a safe path inside its folder is left out of the set: the parse names it.
func documentImageFiles(documents map[string][]byte) (map[string]bool, bool, error) {
	out := map[string]bool{}
	named := false
	names := sortedArchiveNames(documents)
	for _, name := range names {
		doc, err := decodeDocument(name, documents[name])
		if err != nil {
			return nil, false, err
		}
		prefix := path.Dir(name)
		for _, version := range doc.Versions {
			for _, variant := range version.Variants {
				for _, device := range variant.Topology.Devices {
					if device.Image.File == "" {
						continue
					}
					named = true
					clean, linkErr := checkLinkPath(device.Image.File)
					if linkErr != nil {
						continue
					}
					if prefix != "." {
						clean = prefix + "/" + clean
					}
					out[clean] = true
				}
			}
		}
	}
	return out, named, nil
}

// namesImageFiles reads only the documents of an opened archive and tells whether any device names an image file.
func namesImageFiles(entries []zipEntry, protected bool) (bool, error) {
	names, byName, err := checkedEntries(entries, protected)
	if err != nil {
		return false, err
	}
	var budget archiveBudget
	documents, err := readDocuments(names, byName, len(entries), &budget)
	if err != nil {
		return false, err
	}
	_, named, err := documentImageFiles(documents)
	return named, err
}

// ArchiveNamesImages tells whether a task archive (bytes, the password when it is protected) names any device image
// file; such an archive is imported in the background. A broken archive answers false: the synchronous import names
// what is wrong with it.
func ArchiveNamesImages(contents []byte, password string) bool {
	entries, protected, err := openZipAt(bytes.NewReader(contents), int64(len(contents)), password)
	if err != nil {
		return false
	}
	named, err := namesImageFiles(entries, protected)
	return err == nil && named
}

// readArchiveEntries reads an opened archive with its image files left in the zip.
func readArchiveEntries(entries []zipEntry, protected bool) (archiveRead, error) {
	names, byName, err := checkedEntries(entries, protected)
	if err != nil {
		return archiveRead{}, err
	}
	var budget archiveBudget
	files, err := readDocuments(names, byName, len(entries), &budget)
	if err != nil {
		return archiveRead{}, err
	}
	imageNames, _, err := documentImageFiles(files)
	if err != nil {
		return archiveRead{}, err
	}
	images := map[string]*archiveImageFile{}
	var imageTotal int64
	for _, name := range names {
		if _, done := files[name]; done {
			continue
		}
		e := byName[name]
		if !imageNames[name] {
			body, readErr := readEntry(&budget, len(entries), e)
			if readErr != nil {
				return archiveRead{}, readErr
			}
			files[name] = body
			continue
		}
		size := int64(e.uncompressed)
		if e.uncompressed > uint64(imageRegistryModel.MaxImageArchiveBytes) {
			return archiveRead{}, archiveTooLarge("an image file is over " + gib(imageRegistryModel.MaxImageArchiveBytes) + " unpacked")
		}
		if imageTotal += size; imageTotal > imageRegistryModel.MaxImageArchiveBytes {
			return archiveRead{}, archiveTooLarge("the image files are over " + gib(imageRegistryModel.MaxImageArchiveBytes) + " unpacked together")
		}
		images[name] = imageFileOf(name, size, e.open)
	}
	files, err = validateArchiveManifestWith(files, images)
	if err != nil {
		return archiveRead{}, err
	}
	return archiveRead{files: files, images: images, protected: protected}, nil
}

// imageFileOf is an image entry left in the zip, read through sizedEntryReader.
func imageFileOf(name string, size int64, open func(stream bool) (io.ReadCloser, error)) *archiveImageFile {
	return &archiveImageFile{Name: name, Size: size, open: func() (io.ReadCloser, error) {
		rc, err := open(true)
		if err != nil {
			if !isTyped(err) && IsArchiveFormatError(err) {
				return nil, fmt.Errorf("%w: %w", ociregistry.ErrSourceCorrupt, err)
			}
			return nil, err
		}
		return &sizedEntryReader{r: rc, size: size}, nil
	}}
}

func gib(n int64) string { return fmt.Sprintf("%d GiB", n>>30) }

// parseArchiveRead parses every task folder of an archive read with its image files.
func parseArchiveRead(read archiveRead) ([]ParsedExerciseArchive, error) {
	folders, err := splitTaskFolders(read.files)
	if err != nil {
		return nil, err
	}
	tasks := make([]ParsedExerciseArchive, 0, len(folders))
	for _, folder := range folders {
		images := map[string]*archiveImageFile{}
		for name, image := range read.images {
			if folder.Prefix == "" {
				images[name] = image
			} else if rel, ok := strings.CutPrefix(name, folder.Prefix+"/"); ok {
				images[rel] = image
			}
		}
		parsed, parseErr := parseTaskFolder(folder.Files, images)
		if parseErr != nil {
			return nil, parseErr
		}
		tasks = append(tasks, parsed)
	}
	return tasks, nil
}

// importArchiveRead imports every task of an archive read with its image files, all or nothing: every task is
// parsed and checked, every image file indexed and then pushed, and only then are the tasks created. A failure
// while creating removes the tasks this import already created.
func (u *ExerciseUseCase) importArchiveRead(ctx context.Context, read archiveRead, createdBy uuid.UUID) ([]ExerciseView, error) {
	tasks, err := parseArchiveRead(read)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if err = u.prepareImport(ctx, &tasks[i], read.protected); err != nil {
			return nil, err
		}
	}
	refs, err := u.pushArchiveImages(ctx, tasks, createdBy)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if err = replaceImageMarkers(tasks[i].Versions, refs); err != nil {
			return nil, err
		}
	}
	created := make([]ExerciseView, 0, len(tasks))
	for i := range tasks {
		view, storeErr := u.storeImport(ctx, &tasks[i], createdBy)
		if storeErr != nil {
			for _, done := range created {
				_, _ = u.exercises.Delete(context.WithoutCancel(ctx), done.ID)
			}
			return nil, storeErr
		}
		created = append(created, view)
	}
	return created, nil
}

// pushArchiveImages indexes every image file the tasks name (each once), then pushes each; it returns the platform
// reference per marker. An image is named after the first device naming it.
func (u *ExerciseUseCase) pushArchiveImages(ctx context.Context, tasks []ParsedExerciseArchive, createdBy uuid.UUID) (map[string]string, error) {
	var images []ParsedArchiveImage
	seen := map[string]bool{}
	for _, task := range tasks {
		for _, image := range task.Images {
			if !seen[image.Marker] {
				seen[image.Marker] = true
				images = append(images, image)
			}
		}
	}
	if len(images) == 0 {
		return nil, nil
	}
	if u.archiveImages == nil {
		return nil, model.ErrPlatform.WithMessage("Image import is not wired").Err()
	}
	// The registry not configured: the upload refusal, before any image is read.
	if err := u.archiveImages.RequireImageUploads(ctx); err != nil {
		return nil, err
	}
	indexed := make([]*ociregistry.ImageArchive, len(images))
	for i, image := range images {
		archive, err := u.archiveImages.IndexImageArchive(image.file.open, image.file.Size)
		if err != nil {
			return nil, withImageFile(err, image.file.Name)
		}
		if image.file.checksum != "" && archive.SHA256 != image.file.checksum {
			return nil, archiveChecksumMismatch(image.file.Name)
		}
		indexed[i] = archive
	}
	refs := make(map[string]string, len(images))
	for i, image := range images {
		res, err := u.archiveImages.PushImageArchive(ctx, imageRegistryUseCase.PushArchiveInput{UploadedBy: createdBy, Name: image.Device, Archive: indexed[i]})
		if err != nil {
			return nil, withImageFile(err, image.file.Name)
		}
		refs[image.Marker] = res.Reference
	}
	return refs, nil
}

// withImageFile adds the archive path of the image file an error is about (public context "file").
func withImageFile(failure error, file string) error {
	var creator appErr.ErrorCreator
	if !errors.As(failure, &creator) {
		return failure
	}
	return creator.WithPublicContext("file", file).Err()
}

// replaceImageMarkers gives every device holding an image file marker the pushed image's reference.
func replaceImageMarkers(versions []exerciseModel.ExerciseVersion, refs map[string]string) error {
	for vi := range versions {
		for xi := range versions[vi].Variants {
			devices := versions[vi].Variants[xi].Topology.Devices
			for di := range devices {
				if !strings.HasPrefix(devices[di].Image, imageFileMarker) {
					continue
				}
				ref, ok := refs[devices[di].Image]
				if !ok {
					return model.ErrPlatform.WithMessage("An image file of the archive was not pushed").Err()
				}
				devices[di].Image = ref
			}
		}
	}
	return nil
}
