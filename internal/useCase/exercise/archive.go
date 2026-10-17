package exercise

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/gofrs/uuid"
	"io"
	"path"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cybericebox/daemon/internal/model"
	yzip "github.com/yeka/zip"
	"golang.org/x/text/unicode/norm"
)

type archiveManifest struct {
	Version   int               `json:"version"`
	Checksums map[string]string `json:"checksums"`
}
type ExportOptions struct {
	IncludeSecrets bool
	Password       string
}
type exportedFile struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}
type ParsedExerciseArchive struct {
	Exercise exerciseModel.Exercise
	Versions []exerciseModel.ExerciseVersion
	// Files are the task folder's entries, paths relative to it.
	Files    map[string][]byte
	FileMeta map[uuid.UUID]exportedFile
	// FilePaths maps each imported file id to the first folder path linking those bytes.
	FilePaths map[uuid.UUID]string
}

type ImportExerciseInput struct {
	Archive   []byte
	Password  string
	CreatedBy uuid.UUID
}

// ArchiveImport is one normalized exercise payload extracted from either a
// single archive or a catalogue bundle. Password is set only when that
// payload was protected, so an unrelated password form field never makes a
// normal ZIP look like an encrypted envelope.
type ArchiveImport struct {
	Archive  []byte
	Password string
}

type mediaArchiveWriter interface {
	UploadFile(context.Context, string, string, io.Reader, uuid.UUID) (mediaModel.File, error)
}

func ParseExerciseArchive(data []byte, password string) (ParsedExerciseArchive, error) {
	all, err := readExerciseArchive(data, password)
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	folders, err := splitTaskFolders(all)
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	if len(folders) != 1 {
		return ParsedExerciseArchive{}, archiveInvalid("the archive holds several task folders: import it as a bundle")
	}
	files := folders[0].Files
	name, body, err := pickExerciseDocument(files)
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	switch {
	case name == "exercise.yaml":
		body, err = yamlToJSON(body)
	case len(body) > maxExerciseDocBytes: // the same cap as YAML, before any work on the bytes
		err = archiveInvalid("the document is too large")
	default:
		body, err = stripJSONComments(body)
	}
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	if err = probeArchiveSchema(body); err != nil {
		return ParsedExerciseArchive{}, err
	}
	doc, err := decodeArchiveV2(body)
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	return doc.toParsed(files)
}

// ExpandExerciseArchiveBundle splits an archive into one single-task archive per task folder (any depth,
// or the zip root) for ImportExerciseArchive. Each folder is repacked with paths relative to it; when a
// password was given the repacked archive is protected with it.
func ExpandExerciseArchiveBundle(data []byte, password string) ([]ArchiveImport, error) {
	files, err := readExerciseArchive(data, password)
	if err != nil {
		return nil, err
	}
	folders, err := splitTaskFolders(files)
	if err != nil {
		return nil, err
	}
	imports := make([]ArchiveImport, 0, len(folders))
	for _, folder := range folders {
		contents := folder.Files
		archive, buildErr := BuildArchiveV1(contents)
		if buildErr != nil {
			return nil, buildErr
		}
		archivePassword := ""
		if password != "" {
			archive, buildErr = BuildProtectedArchiveV1(contents, password)
			if buildErr != nil {
				return nil, buildErr
			}
			archivePassword = password
		}
		imports = append(imports, ArchiveImport{Archive: archive, Password: archivePassword})
	}
	return imports, nil
}

// ImportExerciseArchive restores one exported exercise as a new local
// identity. Source ids and owners are never reused. Attachments are copied
// through the media use case, which is the only route to object storage.
func (u *ExerciseUseCase) ImportExerciseArchive(ctx context.Context, in ImportExerciseInput) (ExerciseView, error) {
	parsed, err := ParseExerciseArchive(in.Archive, in.Password)
	if err != nil {
		return ExerciseView{}, err
	}
	// A linked variant holds only its own parts in the archive: the base fills in the rest before validation.
	if err = syncImportedLinks(parsed.Versions); err != nil {
		return ExerciseView{}, err
	}
	if err = validateImportedVersions(parsed.Versions); err != nil {
		return ExerciseView{}, err
	}
	if in.Password == "" && containsSecretValues(parsed.Versions) {
		return ExerciseView{}, fmt.Errorf("an archive containing secret values must be password-protected")
	}
	// A secret is bound to its variant id: the ids are final before anything is sealed.
	for i := range parsed.Versions {
		normalizeContentIDs(parsed.Versions[i].Variants)
	}
	// The ids the base just got (tasks, hints, devices, placeholder keys) flow into its linked variants.
	if err = syncImportedLinks(parsed.Versions); err != nil {
		return ExerciseView{}, err
	}
	if err = u.encryptImportedSecrets(parsed.Versions); err != nil {
		return ExerciseView{}, err
	}
	if err = u.copyImportedAttachments(ctx, &parsed, in.CreatedBy); err != nil {
		return ExerciseView{}, err
	}

	created, err := u.createImportedIdentity(ctx, parsed.Exercise, in.CreatedBy)
	if err != nil {
		return ExerciseView{}, err
	}
	// Database writes happen only after archive validation, secret encryption
	// and all media uploads succeeded. On a later database/ref failure, remove
	// the newly-created aggregate; unreferenced media is intentionally left to
	// the media garbage collector rather than risking deletion of deduplicated
	// blobs.
	if err = u.adoptImportedFiles(ctx, created.ID, parsed.Versions); err != nil {
		_, _ = u.exercises.Delete(ctx, created.ID)
		return ExerciseView{}, err
	}
	if err = u.persistImportedVersions(ctx, created.ID, parsed.Versions, in.CreatedBy); err != nil {
		_, _ = u.exercises.Delete(ctx, created.ID)
		return ExerciseView{}, err
	}
	return created, nil
}

// adoptImportedFiles stores the uploaded attachments as the new task's files (each content once), in every
// version, linked variants included: their attachments are their own.
func (u *ExerciseUseCase) adoptImportedFiles(ctx context.Context, exerciseID uuid.UUID, versions []exerciseModel.ExerciseVersion) error {
	for i := range versions {
		if err := u.adoptFiles(ctx, exerciseID, versions[i].Variants); err != nil {
			return err
		}
	}
	return nil
}

// syncImportedLinks rebuilds the linked variants of every version from their bases.
func syncImportedLinks(versions []exerciseModel.ExerciseVersion) error {
	for i := range versions {
		if err := versions[i].SyncLinkedVariants(); err != nil {
			return err
		}
	}
	return nil
}

func containsSecretValues(versions []exerciseModel.ExerciseVersion) bool {
	for _, version := range versions {
		for _, variant := range version.Variants {
			for _, device := range variant.Topology.Devices {
				for _, env := range device.EnvVars {
					if env.Secret && env.Value != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

// validateImportedVersions mirrors the lifecycle rule: only a published
// version must be publishable; drafts and checkpoints may be incomplete, and
// historical rows load as-is.
func validateImportedVersions(versions []exerciseModel.ExerciseVersion) error {
	if len(versions) == 0 {
		return fmt.Errorf("exercise archive has no versions")
	}
	drafts, published := 0, 0
	for index := range versions {
		version := &versions[index]
		if !version.Status.Valid() {
			return fmt.Errorf("exercise archive has an invalid version status")
		}
		if version.Status.IsPublished() {
			published++
			if err := version.ValidateForPublish(); err != nil {
				return err
			}
		}
		if version.Status.IsDraft() {
			drafts++
		}
	}
	if drafts > 1 || published > 1 {
		return fmt.Errorf("exercise archive has conflicting active versions")
	}
	return nil
}

func (u *ExerciseUseCase) copyImportedAttachments(ctx context.Context, parsed *ParsedExerciseArchive, createdBy uuid.UUID) error {
	needed := map[uuid.UUID]bool{}
	for _, version := range parsed.Versions {
		for _, id := range exerciseModel.CollectFileIDs(version.Variants) {
			needed[id] = true
		}
	}
	if len(needed) == 0 {
		return nil
	}
	writer, ok := u.media.(mediaArchiveWriter)
	if !ok {
		return fmt.Errorf("media storage is not available for attachment import")
	}
	// Sorted, so the same archive uploads in the same order (and the first name of a content is stable).
	sources := make([]uuid.UUID, 0, len(needed))
	for sourceID := range needed {
		sources = append(sources, sourceID)
	}
	slices.SortFunc(sources, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	mapped := make(map[uuid.UUID]uuid.UUID, len(needed))
	// Each distinct content goes up once, however many paths carry it.
	uploadedByHash := make(map[[sha256.Size]byte]uuid.UUID, len(needed))
	for _, sourceID := range sources {
		meta, ok := parsed.FileMeta[sourceID]
		if !ok {
			return fmt.Errorf("exercise archive is missing metadata for attachment %s", sourceID)
		}
		body, ok := lookupAttachment(parsed, sourceID)
		if !ok {
			return fmt.Errorf("exercise archive is missing attachment %s", sourceID)
		}
		hash := sha256.Sum256(body)
		if stored, done := uploadedByHash[hash]; done {
			mapped[sourceID] = stored
			continue
		}
		contentType := meta.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		file, uploadErr := writer.UploadFile(ctx, meta.Name, contentType, bytes.NewReader(body), createdBy)
		if uploadErr != nil {
			return uploadErr
		}
		mapped[sourceID] = file.ID
		uploadedByHash[hash] = file.ID
	}
	for versionIndex := range parsed.Versions {
		for variantIndex := range parsed.Versions[versionIndex].Variants {
			for taskIndex := range parsed.Versions[versionIndex].Variants[variantIndex].Tasks {
				attachments := parsed.Versions[versionIndex].Variants[variantIndex].Tasks[taskIndex].Attachments
				for attachmentIndex := range attachments {
					attachments[attachmentIndex].FileID = mapped[attachments[attachmentIndex].FileID]
				}
				parsed.Versions[versionIndex].Variants[variantIndex].Tasks[taskIndex].Attachments = attachments
			}
		}
	}
	return nil
}

func (u *ExerciseUseCase) createImportedIdentity(ctx context.Context, source exerciseModel.Exercise, createdBy uuid.UUID) (ExerciseView, error) {
	baseName := source.Name
	for attempt := 0; attempt < 100; attempt++ {
		name := importedName(baseName, attempt)
		created, err := u.CreateExercise(ctx, CreateExerciseInput{Name: name, Description: source.Description, Tags: source.Tags, CreatedBy: createdBy})
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, exerciseModel.ErrExerciseExists.Err()) {
			return ExerciseView{}, err
		}
	}
	return ExerciseView{}, fmt.Errorf("could not allocate a unique imported exercise name")
}

func importedName(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	// Counted in characters, like the name limit: cutting bytes split a Cyrillic letter and Postgres refused the name.
	suffix := fmt.Sprintf(" (імпорт %d)", attempt)
	base = strings.TrimSpace(truncateRunes(base, 50-utf8.RuneCountInString(suffix)))
	return base + suffix
}

// maxImportedCheckpointLabelRunes mirrors the checkpoint-creation limit
// (checkpointRequest.Note, ≤ 500 runes) but truncates instead of rejecting:
// an archive is already-persisted history, not a fresh user submission.
const maxImportedCheckpointLabelRunes = 500

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func (u *ExerciseUseCase) persistImportedVersions(ctx context.Context, exerciseID uuid.UUID, versions []exerciseModel.ExerciseVersion, createdBy uuid.UUID) error {
	now := time.Now()
	draft, published := uuid.NullUUID{}, uuid.NullUUID{}
	for index := range versions {
		version := versions[index]
		version.ID = uuid.Must(uuid.NewV7())
		version.ExerciseID = exerciseID
		version.CreatedAt = now.Add(time.Duration(index) * time.Nanosecond)
		version.CreatedBy = uuid.NullUUID{UUID: createdBy, Valid: createdBy != uuid.Nil}
		// Label invariant: only a checkpoint carries an explicit snapshot
		// note. A draft/published/unpublished row in the archive must not
		// resurrect a label (e.g. from a tampered or foreign-format
		// archive.json); a checkpoint's label is trimmed and capped, never
		// rejected, since import restores history rather than validating a
		// fresh submission.
		if version.Status.IsCheckpoint() {
			version.Label = truncateRunes(strings.TrimSpace(version.Label), maxImportedCheckpointLabelRunes)
		} else {
			version.Label = ""
		}
		if version.Status.IsPublished() {
			publishedAt := now
			version.PublishedAt = &publishedAt
			published = uuid.NullUUID{UUID: version.ID, Valid: true}
		} else {
			version.PublishedAt = nil
		}
		if version.Status.IsDraft() {
			draft = uuid.NullUUID{UUID: version.ID, Valid: true}
		}
		if _, err := u.exercises.InsertImportedVersion(ctx, version); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to import exercise version").Err()
		}
		if err := u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, version.ID, exerciseModel.CollectFileIDs(version.Variants)); err != nil {
			return err
		}
	}
	if err := u.exercises.SetImportedPointers(ctx, exerciseID, draft, published); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to finalize imported exercise").Err()
	}
	return nil
}

type mediaArchiveReader interface {
	StreamFile(context.Context, uuid.UUID) (io.ReadCloser, mediaModel.File, error)
}

// ExportExerciseArchive writes one task as an archive: exercise.json (schema cib-exercise/1), every attachment
// once per content (SHA-256) at the path assignExportPaths gives it under files/, and manifest.json. With
// secrets the archive is AES-protected and secret values are written in plain text only inside it.
func (u *ExerciseUseCase) ExportExerciseArchive(ctx context.Context, id uuid.UUID, opts ExportOptions) ([]byte, error) {
	e, err := u.exercises.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	versions, err := u.exercises.ListVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	if !opts.IncludeSecrets {
		for i := range versions {
			versions[i].Variants = maskSecrets(versions[i].Variants)
		}
	} else if err = u.decryptExportedSecrets(versions); err != nil {
		return nil, err
	}
	contents, filePaths, err := u.exportAttachments(ctx, versions)
	if err != nil {
		return nil, err
	}
	document, err := buildV2Document(e, versions, filePaths)
	if err != nil {
		return nil, err
	}
	contents["exercise.json"] = document
	if opts.IncludeSecrets {
		return BuildProtectedArchiveV1(contents, opts.Password)
	}
	return BuildArchiveV1(contents)
}

// exportAttachments streams every attached file once and lays the contents out: it returns the archive
// entries and, per file id, the path its attachments link to. Files with equal bytes share one entry. An
// attachment without a shown name gets its stored file's name in versions (the in-memory export copy).
func (u *ExerciseUseCase) exportAttachments(ctx context.Context, versions []exerciseModel.ExerciseVersion) (map[string][]byte, map[uuid.UUID]string, error) {
	contents := map[string][]byte{}
	hashOf := map[uuid.UUID]string{}
	bodies := map[string][]byte{}
	mediaNames := map[uuid.UUID]string{}
	var uses []fileUse
	for vi := range versions {
		for xi := range versions[vi].Variants {
			for ti := range versions[vi].Variants[xi].Tasks {
				attachments := versions[vi].Variants[xi].Tasks[ti].Attachments
				for ai := range attachments {
					ref := &attachments[ai]
					if _, done := hashOf[ref.FileID]; !done {
						hash, body, name, err := u.streamExportedFile(ctx, ref.FileID)
						if err != nil {
							return nil, nil, err
						}
						hashOf[ref.FileID], mediaNames[ref.FileID] = hash, name
						bodies[hash] = body
					}
					// An attachment saved without a shown name shows its file's name: written as such, a re-import
					// shows the same and not the name the file got in the archive.
					if ref.Name == "" {
						ref.Name = mediaNames[ref.FileID]
					}
					uses = append(uses, fileUse{Hash: hashOf[ref.FileID], Name: ref.Name, Version: vi + 1, Variant: xi + 1, Task: ti + 1, Attachment: ai + 1})
				}
			}
		}
	}
	byHash := assignExportPaths("files", uses)
	filePaths := make(map[uuid.UUID]string, len(hashOf))
	for fileID, hash := range hashOf {
		filePaths[fileID] = byHash[hash]
		contents[byHash[hash]] = bodies[hash]
	}
	return contents, filePaths, nil
}

// streamExportedFile reads one stored file: its content hash (the stored one, else computed), bytes and name.
func (u *ExerciseUseCase) streamExportedFile(ctx context.Context, fileID uuid.UUID) (string, []byte, string, error) {
	reader, ok := u.media.(mediaArchiveReader)
	if !ok {
		return "", nil, "", fmt.Errorf("media storage is not available for attachment export")
	}
	rc, file, err := reader.StreamFile(ctx, fileID)
	if err != nil {
		return "", nil, "", err
	}
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return "", nil, "", err
	}
	hash := file.ContentHash
	if hash == "" {
		hash = hashBytes(body)
	}
	return hash, body, file.Name, nil
}

// NamedArchive is one exported task archive and the task name its bundle folder is named after.
type NamedArchive struct {
	Name    string
	Archive []byte
}

// bundleReservedFolders are names a task folder at the zip root must not take: the root manifest and what the
// import drops as macOS litter (compared in lower case).
var bundleReservedFolders = []string{"manifest.json", "__macosx"}

// BundleExerciseArchives puts several exported tasks into one archive: each in a folder named exportName(task
// name) (a taken name gets _2, _3), one manifest.json at the root. A single task stays at the zip root. With a
// password every archive is read and the bundle written protected, so plain secrets never leave encryption.
func BundleExerciseArchives(items []NamedArchive, password string) ([]byte, error) {
	if len(items) == 1 {
		return items[0].Archive, nil
	}
	taken := map[string]bool{}
	for _, name := range bundleReservedFolders {
		taken[name] = true
	}
	bundle := map[string][]byte{}
	for _, item := range items {
		var files map[string][]byte
		var err error
		if password != "" {
			files, err = ReadProtectedArchiveV1(item.Archive, password)
		} else {
			files, err = ReadArchiveV1(bytes.NewReader(item.Archive))
		}
		if err != nil {
			return nil, err
		}
		folder := freeName(exportName(item.Name), taken)
		for name, body := range files {
			bundle[folder+"/"+name] = body
		}
	}
	if password != "" {
		return BuildProtectedArchiveV1(bundle, password)
	}
	return BuildArchiveV1(bundle)
}

// BuildArchiveV1 creates the portable, checksummed ZIP envelope. Callers own
// the exercise serialization and media streaming; this codec never knows S3.
func BuildArchiveV1(files map[string][]byte) ([]byte, error) {
	return buildArchive(files, true)
}

// BuildArchiveNoManifest builds the same ZIP without manifest.json: what an author writes by hand.
func BuildArchiveNoManifest(files map[string][]byte) ([]byte, error) {
	return buildArchive(files, false)
}

func buildArchive(files map[string][]byte, withManifest bool) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	checks := map[string]string{}
	names := make([]string, 0, len(files))
	for n := range files {
		if !safeArchivePath(n) {
			return nil, fmt.Errorf("unsafe archive path")
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b := files[n]
		checks[n] = hashBytes(b)
		w, e := zw.Create(n)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(b); e != nil {
			return nil, e
		}
	}
	if withManifest {
		manifest, e := json.Marshal(archiveManifest{Version: 1, Checksums: checks})
		if e != nil {
			return nil, e
		}
		w, e := zw.Create("manifest.json")
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(manifest); e != nil {
			return nil, e
		}
	}
	if e := zw.Close(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}

// Limits of one archive: the entries, each entry, everything unpacked together, and how far a compressed entry
// may expand. A small archive of repeated bytes unpacks into gigabytes otherwise.
const (
	maxArchiveEntries      = 1000
	maxArchiveEntryBytes   = 64 << 20
	maxArchiveTotalBytes   = 256 << 20
	maxArchiveRatio        = 200
	archiveRatioFreeBytes  = 1 << 20
	errArchiveTooLargeText = "archive is too large when unpacked"
)

// archiveBudget tracks what one archive has unpacked so far.
type archiveBudget struct{ total int64 }

// check refuses an entry the header already shows to be too large or expanding too far, before it is read.
func (b *archiveBudget) check(entries int, uncompressed, compressed uint64) error {
	if entries > maxArchiveEntries {
		return fmt.Errorf("archive has more than %d entries", maxArchiveEntries)
	}
	if uncompressed > maxArchiveEntryBytes {
		return fmt.Errorf("archive entry exceeds 64 MiB")
	}
	if uncompressed > archiveRatioFreeBytes && (compressed == 0 || uncompressed/compressed > maxArchiveRatio) {
		return fmt.Errorf(errArchiveTooLargeText)
	}
	return nil
}

// read unpacks one entry within the limits; the header's sizes are not trusted, the bytes actually read count.
func (b *archiveBudget) read(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxArchiveEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxArchiveEntryBytes {
		return nil, fmt.Errorf("archive entry exceeds 64 MiB")
	}
	if b.total += int64(len(body)); b.total > maxArchiveTotalBytes {
		return nil, fmt.Errorf(errArchiveTooLargeText)
	}
	return body, nil
}

func ReadArchiveV1(r io.Reader) (map[string][]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, 128<<20))
	if e != nil {
		return nil, e
	}
	zr, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		return nil, e
	}
	out := map[string][]byte{}
	var budget archiveBudget
	for _, f := range zr.File {
		name, skip, entryErr := checkArchiveEntry(f.Name, f.Mode(), out)
		if entryErr != nil {
			return nil, entryErr
		}
		if skip {
			continue
		}
		if e = budget.check(len(zr.File), f.UncompressedSize64, f.CompressedSize64); e != nil {
			return nil, e
		}
		rc, e := f.Open()
		if e != nil {
			return nil, e
		}
		d, e := budget.read(rc)
		rc.Close()
		if e != nil {
			return nil, e
		}
		out[name] = d
	}
	return validateArchiveManifest(out)
}

// BuildProtectedArchiveV1 emits a regular AES-encrypted ZIP. Every entry,
// including manifest and attachments, shares one password; no plaintext data
// is left alongside an encrypted sidecar.
func BuildProtectedArchiveV1(files map[string][]byte, password string) ([]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("password is required for protected archive")
	}
	withManifest := archiveFilesWithManifest(files)
	var out bytes.Buffer
	zw := yzip.NewWriter(&out)
	names := sortedArchiveNames(withManifest)
	for _, name := range names {
		writer, err := zw.Encrypt(name, password, yzip.AES256Encryption)
		if err != nil {
			return nil, err
		}
		if _, err = writer.Write(withManifest[name]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func readExerciseArchive(data []byte, password string) (map[string][]byte, error) {
	files, err := ReadArchiveV1(bytes.NewReader(data))
	if err == nil {
		return files, nil
	}
	// A checksum mismatch means a readable archive edited by hand: a password would not help.
	if password == "" || errors.Is(err, exerciseModel.ErrArchiveChecksumMismatch.Err()) {
		return nil, err
	}
	return ReadProtectedArchiveV1(data, password)
}

func ReadProtectedArchiveV1(data []byte, password string) (map[string][]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("password is required for protected archive")
	}
	if len(data) > 128<<20 {
		return nil, fmt.Errorf("archive exceeds 128 MiB")
	}
	zr, err := yzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var budget archiveBudget
	for _, file := range zr.File {
		name, skip, entryErr := checkArchiveEntry(file.Name, file.Mode(), files)
		if entryErr != nil {
			return nil, entryErr
		}
		if skip {
			continue
		}
		if !file.IsEncrypted() {
			return nil, fmt.Errorf("protected archive contains an unencrypted entry")
		}
		if err = budget.check(len(zr.File), file.UncompressedSize64, file.CompressedSize64); err != nil {
			return nil, err
		}
		file.SetPassword(password)
		reader, openErr := file.Open()
		if openErr != nil {
			return nil, openErr
		}
		body, readErr := budget.read(reader)
		_ = reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		files[name] = body
	}
	return validateArchiveManifest(files)
}

func archiveFilesWithManifest(files map[string][]byte) map[string][]byte {
	withManifest := make(map[string][]byte, len(files)+1)
	checksums := make(map[string]string, len(files))
	for name, body := range files {
		withManifest[name] = body
		checksums[name] = hashBytes(body)
	}
	withManifest["manifest.json"], _ = json.Marshal(archiveManifest{Version: 1, Checksums: checksums})
	return withManifest
}

func sortedArchiveNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validateArchiveManifest verifies every manifest.json in the archive and removes it from the result. No
// manifest means nothing to verify. A manifest at the root covers every entry by its full path; one inside a
// task folder (older bundles) covers that folder, its paths relative to it. A manifest.json elsewhere, or in a
// folder without exercise.json / exercise.yaml, is an ordinary attachment and stays untouched. A manifest lists
// what to check: an entry it does not list is accepted unverified, a listed entry that is missing or differs
// is a checksum mismatch.
func validateArchiveManifest(files map[string][]byte) (map[string][]byte, error) {
	var manifests []string
	for name := range files {
		if isArchiveManifest(name, files) {
			manifests = append(manifests, name)
		}
	}
	sort.Strings(manifests)
	for _, name := range manifests {
		var manifest archiveManifest
		if err := json.Unmarshal(files[name], &manifest); err != nil {
			return nil, err
		}
		if manifest.Version != 1 {
			return nil, fmt.Errorf("unsupported archive version")
		}
		prefix := strings.TrimSuffix(name, "manifest.json")
		checked := make([]string, 0, len(manifest.Checksums))
		for entry := range manifest.Checksums {
			checked = append(checked, entry)
		}
		sort.Strings(checked)
		for _, entry := range checked {
			// Entry names are stored in NFC (checkArchiveEntry); a manifest written on macOS may list NFD.
			name := norm.NFC.String(prefix + entry)
			if got, ok := files[name]; !ok || hashBytes(got) != manifest.Checksums[entry] {
				return nil, archiveChecksumMismatch(name)
			}
		}
	}
	for _, name := range manifests {
		delete(files, name)
	}
	return files, nil
}

// isArchiveManifest tells the archive manifest from an attachment that happens to be named manifest.json.
func isArchiveManifest(name string, files map[string][]byte) bool {
	if name == "manifest.json" {
		return true
	}
	dir, ok := strings.CutSuffix(name, "/manifest.json")
	if !ok {
		return false
	}
	_, hasJSON := files[dir+"/exercise.json"]
	_, hasYAML := files[dir+"/exercise.yaml"]
	return hasJSON || hasYAML
}

// archiveChecksumMismatch is the one place ErrArchiveChecksumMismatch is raised; file is the entry that differs.
func archiveChecksumMismatch(file string) error {
	return exerciseModel.ErrArchiveChecksumMismatch.WithPublicContext("file", file).Err()
}

func safeArchivePath(n string) bool {
	return n != "" && n != "." && n != ".." && n == path.Clean(n) && !strings.HasPrefix(n, "/") && !strings.HasPrefix(n, "../") && !strings.Contains(n, "\\") && n != "manifest.json"
}
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
