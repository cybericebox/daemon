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
	appErr "github.com/cybericebox/daemon/pkg/err"
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
	parsed, _, err := parseExerciseArchive(data, password)
	return parsed, err
}

// parseExerciseArchive also tells whether the archive was password-protected (a typed password alone does not count).
func parseExerciseArchive(data []byte, password string) (ParsedExerciseArchive, bool, error) {
	all, protected, err := readExerciseArchive(data, password)
	if err != nil {
		return ParsedExerciseArchive{}, false, err
	}
	parsed, err := parseTaskFiles(all)
	return parsed, protected, err
}

func parseTaskFiles(all map[string][]byte) (ParsedExerciseArchive, error) {
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
// password opened the source archive the repacked one is protected with it; a password typed for an
// unprotected archive is dropped.
func ExpandExerciseArchiveBundle(data []byte, password string) ([]ArchiveImport, error) {
	files, protected, err := readExerciseArchive(data, password)
	if err != nil {
		return nil, err
	}
	folders, err := splitTaskFolders(files)
	if err != nil {
		return nil, err
	}
	imports := make([]ArchiveImport, 0, len(folders))
	for _, folder := range folders {
		item := ArchiveImport{}
		var buildErr error
		if protected {
			item.Archive, buildErr = BuildProtectedArchiveV1(folder.Files, password)
			item.Password = password
		} else {
			item.Archive, buildErr = BuildArchiveV1(folder.Files)
		}
		if buildErr != nil {
			return nil, buildErr
		}
		imports = append(imports, item)
	}
	return imports, nil
}

// ImportExerciseArchive restores one exported exercise as a new local
// identity. Source ids and owners are never reused. Attachments are copied
// through the media use case, which is the only route to object storage.
func (u *ExerciseUseCase) ImportExerciseArchive(ctx context.Context, in ImportExerciseInput) (ExerciseView, error) {
	parsed, protected, err := parseExerciseArchive(in.Archive, in.Password)
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
	// Only an archive that really was protected may carry secret values: a typed password proves nothing.
	if secretPath := firstSecretValue(parsed.Versions); !protected && secretPath != "" {
		return ExerciseView{}, archiveInvalid(secretPath + ": a secret value needs a password-protected archive")
	}
	// A secret is bound to its variant id: the ids are final before anything is sealed.
	for i := range parsed.Versions {
		normalizeContentIDs(parsed.Versions[i].Variants)
	}
	// The ids the base just got (tasks, hints, devices, placeholder keys) flow into its linked variants.
	if err = syncImportedLinks(parsed.Versions); err != nil {
		return ExerciseView{}, err
	}
	// A version imported as published passes the same gate as PublishDraft, before anything is uploaded or written.
	if err = u.requireImportedPublishable(ctx, parsed.Versions); err != nil {
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

// firstSecretValue gives the 1-based path of the first secret env value, or "" when there is none.
func firstSecretValue(versions []exerciseModel.ExerciseVersion) string {
	for vi, version := range versions {
		for xi, variant := range version.Variants {
			for di, device := range variant.Topology.Devices {
				for ei, env := range device.EnvVars {
					if env.Secret && env.Value != "" {
						return fmt.Sprintf("versions[%d].variants[%d].topology.devices[%d].env_vars[%d]", vi+1, xi+1, di+1, ei+1)
					}
				}
			}
		}
	}
	return ""
}

// validateImportedVersions checks the statuses: at least one version, at most one draft and one published.
// Drafts and checkpoints may be incomplete and historical rows load as-is; the published one passes the publish
// gate later (requireImportedPublishable), once its ids are final.
func validateImportedVersions(versions []exerciseModel.ExerciseVersion) error {
	if len(versions) == 0 {
		return archiveInvalid("versions: at least one version is required")
	}
	// Status first, so a second draft or published version is named before any publish check runs.
	drafts, published := 0, 0
	for index := range versions {
		status := versions[index].Status
		path := fmt.Sprintf("versions[%d].status", index+1)
		switch {
		case !status.Valid():
			return unknownEnum(path, string(status))
		case status.IsDraft():
			drafts++
		case status.IsPublished():
			published++
		}
		if drafts > 1 || published > 1 {
			return archiveInvalid(fmt.Sprintf("%s: only one %s version is allowed", path, status))
		}
	}
	return nil
}

// requireImportedPublishable runs the publish gate (requirePublishable, the one PublishDraft runs) on every
// version imported as published. The import always creates a new catalog task: no owner event, no approved
// elevation yet. A refusal keeps its error and gains the archive path it is about.
func (u *ExerciseUseCase) requireImportedPublishable(ctx context.Context, versions []exerciseModel.ExerciseVersion) error {
	newTask := exerciseModel.Exercise{Scope: exerciseModel.ScopeCatalog}
	for index := range versions {
		if !versions[index].Status.IsPublished() {
			continue
		}
		if err := u.requirePublishable(ctx, newTask, &versions[index]); err != nil {
			return withImportedPath(err, index, versions[index].Variants)
		}
	}
	return nil
}

// withImportedPath adds the archive path to a publish-gate error as its public "field": the version, and the
// device when the error names one variant's device. The variant id is dropped: the new task's ids mean nothing
// to the author of the archive.
func withImportedPath(failure error, versionIndex int, variants []exerciseModel.Variant) error {
	var creator appErr.ErrorCreator
	if !errors.As(failure, &creator) {
		return failure
	}
	var withContext interface{ PublicContext() map[string]any }
	field := fmt.Sprintf("versions[%d]", versionIndex+1)
	if errors.As(failure, &withContext) {
		public := withContext.PublicContext()
		if variantID, ok := public["variant"].(string); ok {
			field += variantPath(variants, variantID, public["device"])
			creator = creator.WithPublicContext("variant", "")
		}
	}
	return creator.WithPublicContext("field", field).Err()
}

// variantPath is ".variants[i]" (and ".topology.devices[j]" for a named device) of the variant with that id.
func variantPath(variants []exerciseModel.Variant, variantID string, device any) string {
	for vi, variant := range variants {
		if variant.ID.String() != variantID {
			continue
		}
		path := fmt.Sprintf(".variants[%d]", vi+1)
		if name, ok := device.(string); ok {
			for di, d := range variant.Topology.Devices {
				if d.Name == name {
					return path + fmt.Sprintf(".topology.devices[%d]", di+1)
				}
			}
		}
		return path
	}
	return ""
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
	if err = checkExportEntries(contents); err != nil {
		return nil, err
	}
	var archive []byte
	if opts.IncludeSecrets {
		archive, err = BuildProtectedArchiveV1(contents, opts.Password)
	} else {
		archive, err = BuildArchiveV1(contents)
	}
	if err != nil {
		return nil, err
	}
	return archive, checkArchiveSize(len(archive))
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
	// Stops at the first file past the archive limits, before the rest is read into memory.
	var budget archiveBudget
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
						if _, seen := bodies[hash]; !seen {
							if err = budget.add(len(body)); err != nil {
								return nil, nil, err
							}
						}
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
	if err := checkExportEntries(bundle); err != nil {
		return nil, err
	}
	var archive []byte
	var err error
	if password != "" {
		archive, err = BuildProtectedArchiveV1(bundle, password)
	} else {
		archive, err = BuildArchiveV1(bundle)
	}
	if err != nil {
		return nil, err
	}
	return archive, checkArchiveSize(len(archive))
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

func ReadArchiveV1(r io.Reader) (map[string][]byte, error) {
	b, e := ReadArchiveBytes(r)
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

// readExerciseArchive reads a plain archive, or a protected one when a password is given; protected tells which.
func readExerciseArchive(data []byte, password string) (files map[string][]byte, protected bool, err error) {
	files, err = ReadArchiveV1(bytes.NewReader(data))
	if err == nil {
		return files, false, nil
	}
	// A checksum mismatch means a readable archive edited by hand, and a password does not make an archive
	// smaller: neither is helped by trying the password.
	if password == "" || errors.Is(err, exerciseModel.ErrArchiveChecksumMismatch.Err()) || errors.Is(err, exerciseModel.ErrArchiveTooLarge.Err()) {
		return nil, false, err
	}
	files, err = ReadProtectedArchiveV1(data, password)
	if err != nil {
		return nil, false, err
	}
	return files, true, nil
}

func ReadProtectedArchiveV1(data []byte, password string) (map[string][]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("password is required for protected archive")
	}
	if err := checkArchiveSize(len(data)); err != nil {
		return nil, err
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
