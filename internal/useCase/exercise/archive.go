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
	"sort"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/model"
	yzip "github.com/yeka/zip"
)

type archiveManifest struct {
	Version   int               `json:"version"`
	Checksums map[string]string `json:"checksums"`
}
type ExportOptions struct {
	IncludeSecrets bool
	Password       string
}
type exportedExercise struct {
	Format   string                          `json:"format"`
	Exercise exerciseModel.Exercise          `json:"exercise"`
	Versions []exerciseModel.ExerciseVersion `json:"versions"`
	Files    map[string]exportedFile         `json:"files,omitempty"`
}
type exportedFile struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}
type ParsedExerciseArchive struct {
	Exercise exerciseModel.Exercise
	Versions []exerciseModel.ExerciseVersion
	Files    map[string][]byte
	FileMeta map[uuid.UUID]exportedFile
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
	files, err := readExerciseArchive(data, password)
	if err != nil {
		return ParsedExerciseArchive{}, err
	}
	payload, ok := files["exercise.json"]
	if !ok {
		return ParsedExerciseArchive{}, fmt.Errorf("exercise archive is missing exercise.json")
	}
	var value exportedExercise
	if err = json.Unmarshal(payload, &value); err != nil {
		return ParsedExerciseArchive{}, err
	}
	if value.Format != "cib-exercise/v1" {
		return ParsedExerciseArchive{}, fmt.Errorf("unsupported exercise archive format")
	}
	metadata := make(map[uuid.UUID]exportedFile, len(value.Files))
	for rawID, file := range value.Files {
		id, parseErr := uuid.FromString(rawID)
		if parseErr != nil {
			return ParsedExerciseArchive{}, fmt.Errorf("invalid attachment metadata: %w", parseErr)
		}
		metadata[id] = file
	}
	return ParsedExerciseArchive{Exercise: value.Exercise, Versions: value.Versions, Files: files, FileMeta: metadata}, nil
}

// ExpandExerciseArchiveBundle turns the folder layout used by bulk export
// into the single-exercise archives accepted by ImportExerciseArchive. A
// protected folder combines its public JSON, attachments and encrypted secret
// JSON back into a fresh password-protected archive in memory.
func ExpandExerciseArchiveBundle(data []byte, password string) ([]ArchiveImport, error) {
	files, err := readExerciseArchive(data, password)
	if err != nil {
		return nil, err
	}
	if _, ok := files["exercise.json"]; ok {
		return []ArchiveImport{{Archive: data, Password: password}}, nil
	}
	folders := map[string]map[string][]byte{}
	for name, body := range files {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid catalogue archive entry: %s", name)
		}
		if folders[parts[0]] == nil {
			folders[parts[0]] = map[string][]byte{}
		}
		folders[parts[0]][parts[1]] = body
	}
	if len(folders) == 0 {
		return nil, fmt.Errorf("catalogue archive contains no exercise folders")
	}
	names := make([]string, 0, len(folders))
	for name := range folders {
		names = append(names, name)
	}
	sort.Strings(names)
	imports := make([]ArchiveImport, 0, len(names))
	for _, folder := range names {
		contents := folders[folder]
		if _, ok := contents["exercise.json"]; !ok {
			return nil, fmt.Errorf("exercise folder %s is missing exercise.json", folder)
		}
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
	if err = u.persistImportedVersions(ctx, created.ID, parsed.Versions, in.CreatedBy); err != nil {
		_, _ = u.exercises.Delete(ctx, created.ID)
		return ExerciseView{}, err
	}
	return created, nil
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
	mapped := make(map[uuid.UUID]uuid.UUID, len(needed))
	for sourceID := range needed {
		meta, ok := parsed.FileMeta[sourceID]
		if !ok {
			return fmt.Errorf("exercise archive is missing metadata for attachment %s", sourceID)
		}
		body, ok := findArchivedFile(parsed.Files, sourceID)
		if !ok {
			return fmt.Errorf("exercise archive is missing attachment %s", sourceID)
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

func findArchivedFile(files map[string][]byte, id uuid.UUID) ([]byte, bool) {
	prefix := "files/" + id.String() + "/"
	for name, body := range files {
		if strings.HasPrefix(name, prefix) {
			return body, true
		}
	}
	return nil, false
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
	suffix := fmt.Sprintf(" (imported %d)", attempt)
	maxBase := 50 - len(suffix)
	if len(base) > maxBase {
		base = strings.TrimSpace(base[:maxBase])
	}
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

// ExportExerciseArchive serializes all immutable version snapshots. Attachments
// are represented by their logical references here; media byte streaming is
// deliberately a separate layer so this function never sees object storage.
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
	contents := map[string][]byte{}
	fileMetadata := map[string]exportedFile{}
	if reader, ok := u.media.(mediaArchiveReader); ok {
		seen := map[uuid.UUID]bool{}
		for _, version := range versions {
			for _, fileID := range exerciseModel.CollectFileIDs(version.Variants) {
				seen[fileID] = true
			}
		}
		for fileID := range seen {
			rc, file, streamErr := reader.StreamFile(ctx, fileID)
			if streamErr != nil {
				return nil, streamErr
			}
			body, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr != nil {
				return nil, readErr
			}
			name := path.Base(file.Name)
			if name == "." || name == "/" || name == "" {
				name = "attachment"
			}
			contents["files/"+fileID.String()+"/"+name] = body
			fileMetadata[fileID.String()] = exportedFile{Name: file.Name, ContentType: file.ContentType}
		}
	}
	payload, err := json.Marshal(exportedExercise{Format: "cib-exercise/v1", Exercise: e, Versions: versions, Files: fileMetadata})
	if err != nil {
		return nil, err
	}
	contents["exercise.json"] = payload
	archive, err := BuildArchiveV1(contents)
	if err != nil {
		return nil, err
	}
	if opts.IncludeSecrets {
		return BuildProtectedArchiveV1(contents, opts.Password)
	}
	return archive, nil
}

// BuildArchiveV1 creates the portable, checksummed ZIP envelope. Callers own
// the exercise serialization and media streaming; this codec never knows S3.
func BuildArchiveV1(files map[string][]byte) ([]byte, error) {
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
	if e = zw.Close(); e != nil {
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
	var m archiveManifest
	var budget archiveBudget
	for _, f := range zr.File {
		if !safeArchivePath(f.Name) && f.Name != "manifest.json" {
			return nil, fmt.Errorf("unsafe archive path")
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
		if f.Name == "manifest.json" {
			if e = json.Unmarshal(d, &m); e != nil {
				return nil, e
			}
		} else {
			out[f.Name] = d
		}
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("unsupported archive version")
	}
	for n, want := range m.Checksums {
		if got, ok := out[n]; !ok || hashBytes(got) != want {
			return nil, fmt.Errorf("checksum mismatch: %s", n)
		}
	}
	return out, nil
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
	if password == "" {
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
		if !safeArchivePath(file.Name) && file.Name != "manifest.json" {
			return nil, fmt.Errorf("unsafe archive path")
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
		files[file.Name] = body
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

func validateArchiveManifest(files map[string][]byte) (map[string][]byte, error) {
	manifestBytes, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("archive is missing manifest")
	}
	var manifest archiveManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, err
	}
	delete(files, "manifest.json")
	if manifest.Version != 1 {
		return nil, fmt.Errorf("unsupported archive version")
	}
	for name, want := range manifest.Checksums {
		if got, ok := files[name]; !ok || hashBytes(got) != want {
			return nil, fmt.Errorf("checksum mismatch: %s", name)
		}
	}
	return files, nil
}
func safeArchivePath(n string) bool {
	return n != "" && n == path.Clean(n) && !strings.HasPrefix(n, "/") && !strings.HasPrefix(n, "../") && !strings.Contains(n, "\\") && n != "manifest.json"
}
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
