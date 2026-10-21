package exercise

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	appErr "github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/pagination"
	"github.com/gofrs/uuid"
)

type archiveFileMetadata interface {
	GetFile(context.Context, uuid.UUID) (mediaModel.File, error)
}
type archiveImageInspector interface {
	InspectImageArchive(context.Context, *ociregistry.ImageArchive, string) (imageRegistryUseCase.ArchiveImageInspection, error)
}

// ListImportTargets applies write policy in SQL before keyset pagination. The
// same policy is checked again for an actual preview target, then at commit.
func (u *ExerciseUseCase) ListImportTargets(ctx context.Context, actor Actor, search string, cursor uuid.UUID, limit int) (pagination.CursorPage[ImportTargetExercise], error) {
	if err := u.RequireAuthor(ctx, actor); err != nil {
		return pagination.CursorPage[ImportTargetExercise]{}, err
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > pagination.MaxPageSize {
		return pagination.CursorPage[ImportTargetExercise]{}, pagination.ErrInvalidPageSize
	}
	params := exerciseRepo.ImportTargetParams{WriterAll: actor.has(rbac.PermExercisesWrite), ViewerID: actor.UserID, Search: strings.TrimSpace(search), CursorID: cursor, Limit: int32(limit + 1)}
	rows, err := u.exercises.ImportTargets(ctx, params)
	if err != nil {
		return pagination.CursorPage[ImportTargetExercise]{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list import targets").Err()
	}
	total, err := u.exercises.CountImportTargets(ctx, params)
	if err != nil {
		return pagination.CursorPage[ImportTargetExercise]{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count import targets").Err()
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	items := make([]ImportTargetExercise, 0, len(rows))
	next := uuid.Nil
	for _, row := range rows {
		items = append(items, ImportTargetExercise{ID: row.ID, Name: row.Name, Scope: importScope(row.Scope), OwnerEventID: uuidPtr(row.OwnerEventID), OwnerEventName: row.OwnerEventName, OwnerEventTag: row.OwnerEventTag, HasDraft: row.HasDraft})
		next = row.ID
	}
	return pagination.NewCursorPage(items, more, next, total), nil
}

func importScope(scope exerciseModel.Scope) string {
	if scope == exerciseModel.ScopeEvent {
		return "event"
	}
	return "catalog"
}

// PreviewArchiveImport is deliberately read-only: it reads an owned immutable
// source, validates the entire archive, and compares selected working snapshots.
// No media claim, task save, queue write or image push occurs on this path.
func (u *ExerciseUseCase) PreviewArchiveImport(ctx context.Context, actor Actor, in PreviewArchiveInput) (ArchiveImportPreview, error) {
	if err := u.RequireAuthor(ctx, actor); err != nil {
		return ArchiveImportPreview{}, err
	}
	reader, size, err := u.previewArchiveSource(ctx, actor, in)
	if err != nil {
		return ArchiveImportPreview{}, err
	}
	lim := u.ArchiveLimits(ctx)
	if err = checkArchiveSize(size, lim.WithImagesBytes()); err != nil {
		return ArchiveImportPreview{}, err
	}
	entries, protected, err := openZipAt(reader, size, in.Password, lim)
	if err != nil {
		return ArchiveImportPreview{}, err
	}
	read, err := readArchiveEntries(entries, protected, lim)
	if err != nil {
		return ArchiveImportPreview{}, err
	}
	tasks, err := parseArchiveRead(read)
	if err != nil {
		return ArchiveImportPreview{}, err
	}
	for i := range tasks {
		if err = u.validateImportContent(&tasks[i], protected); err != nil {
			return ArchiveImportPreview{}, importItemError(err, tasks[i].ItemKey, "")
		}
		// The archive's published snapshots must remain valid portable snapshots.
		// Target resource/elevation checks belong to its eventual Publish, not SaveDraft.
		for vi := range tasks[i].Versions {
			v := &tasks[i].Versions[vi]
			if v.Status.IsPublished() {
				if err = v.ValidateForPublish(); err != nil {
					return ArchiveImportPreview{}, importItemError(withImportedPath(err, vi, v.Variants), tasks[i].ItemKey, "")
				}
			}
		}
	}
	choices, err := previewChoices(tasks, in)
	if err != nil {
		return ArchiveImportPreview{}, err
	}
	imageInfo, imageHashes, err := u.inspectImportImages(ctx, tasks)
	if err != nil {
		return ArchiveImportPreview{}, err
	}
	sourceFingerprint := previewSourceFingerprint(read, imageHashes)
	result := ArchiveImportPreview{Items: make([]ArchiveImportPreviewItem, 0, len(tasks))}
	for i := range tasks {
		parsed := &tasks[i]
		selected := workingImportVersion(parsed.Versions)
		source := exerciseModel.ExerciseVersion{Name: parsed.Exercise.Name, Description: parsed.Exercise.Description, Tags: parsed.Exercise.Tags}
		if selected >= 0 {
			source = parsed.Versions[selected]
		}
		item := ArchiveImportPreviewItem{ItemKey: parsed.ItemKey, Name: source.Name, SourceVersion: ImportSourceVersion{Position: selected, Status: source.Status}}
		if parsed.SourceExerciseID != uuid.Nil {
			id := parsed.SourceExerciseID
			item.SourceExerciseID = &id
		}
		item.SuggestedTarget = ImportTarget{ItemKey: parsed.ItemKey, Mode: "new"}
		var targetEntity exerciseModel.Exercise
		var targetVersion exerciseModel.ExerciseVersion
		// Only an initial suggestion may fall back from a denied/missing/frozen ID.
		if parsed.SourceExerciseID != uuid.Nil && in.FixedExerciseID == uuid.Nil {
			e, v, loadErr := u.previewTarget(ctx, actor, parsed.SourceExerciseID)
			if loadErr == nil {
				item.SuggestedTarget.Mode = "update"
				item.SuggestedTarget.ExerciseID = e.ID
				targetEntity, targetVersion = e, v
			} else if !errors.Is(loadErr, exerciseModel.ErrExerciseForbidden.Err()) && !errors.Is(loadErr, exerciseModel.ErrExerciseNotFound.Err()) && !errors.Is(loadErr, exerciseModel.ErrExerciseArchived.Err()) {
				return ArchiveImportPreview{}, importItemError(loadErr, parsed.ItemKey, "target")
			}
		}
		item.Target = item.SuggestedTarget
		if choice, ok := choices[parsed.ItemKey]; ok {
			item.Target = choice
		}
		if item.Target.Mode == "update" {
			if targetEntity.ID != item.Target.ExerciseID {
				targetEntity, targetVersion, err = u.previewTarget(ctx, actor, item.Target.ExerciseID)
				if err != nil {
					return ArchiveImportPreview{}, importItemError(err, parsed.ItemKey, "target")
				}
			}
			if err = u.requireInfrastructureAllowed(ctx, targetEntity, source.Variants); err != nil {
				return ArchiveImportPreview{}, importItemError(err, parsed.ItemKey, "target")
			}
			info := ImportTargetExercise{ID: targetEntity.ID, Name: targetVersion.Name, Scope: importScope(targetEntity.Scope), OwnerEventID: uuidPtr(targetEntity.OwnerEventID), HasDraft: targetEntity.DraftVersionID.Valid}
			if targetEntity.IsEventScoped() {
				info.OwnerEventName, info.OwnerEventTag, err = u.exercises.ImportTargetContext(ctx, targetEntity.ID)
				if err != nil {
					return ArchiveImportPreview{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read import target context").Err()
				}
			}
			item.TargetExercise = &info
			item.ReplacesDraft = targetEntity.DraftVersionID.Valid
		} else {
			// A user may choose NEW after an editable ID auto-match. That
			// suggestion is not the selected destination or comparison baseline.
			targetEntity, targetVersion = exerciseModel.Exercise{}, exerciseModel.ExerciseVersion{}
			if in.Targets != nil {
				if err = u.AuthorizeImport(ctx, actor, in.OwnerEventID); err != nil {
					return ArchiveImportPreview{}, importItemError(err, parsed.ItemKey, "target")
				}
				if err = u.requireImportedPublishable(ctx, parsed.Versions, in.OwnerEventID); err != nil {
					return ArchiveImportPreview{}, importItemError(err, parsed.ItemKey, "")
				}
			}
		}
		item.SourceIDMismatch = in.FixedExerciseID != uuid.Nil && parsed.SourceExerciseID != uuid.Nil && parsed.SourceExerciseID != in.FixedExerciseID
		item.ConfirmationRevision = previewRevision(in, sourceFingerprint, parsed.ItemKey, selected, item.Target, targetEntity, targetVersion)
		item.Summary, err = u.importSummary(ctx, *parsed, source, targetVersion, imageInfo)
		if err != nil {
			return ArchiveImportPreview{}, importItemError(err, parsed.ItemKey, "")
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (u *ExerciseUseCase) previewArchiveSource(ctx context.Context, actor Actor, in PreviewArchiveInput) (io.ReaderAt, int64, error) {
	if (in.UploadID == uuid.Nil) == (in.FileID == uuid.Nil) {
		return nil, 0, exerciseModel.ErrImportSourceInvalid.Err()
	}
	if u.archiveSources == nil {
		return nil, 0, model.ErrPlatform.WithMessage("Archive import is not wired").Err()
	}
	if in.UploadID != uuid.Nil {
		upload, err := u.archiveSources.UploadStatus(ctx, in.UploadID, actor.UserID)
		if err != nil {
			return nil, 0, err
		}
		if upload.Purpose != mediaModel.UploadPurposeExerciseArchive || !upload.Complete() {
			return nil, 0, exerciseModel.ErrArchiveUploadNotReady.Err()
		}
		reader, _, err := u.archiveSources.OpenUploadAt(ctx, in.UploadID)
		return reader, upload.SizeBytes, err
	}
	metadata, ok := u.archiveSources.(archiveFileMetadata)
	if !ok {
		return nil, 0, model.ErrPlatform.WithMessage("Archive file metadata is not wired").Err()
	}
	file, err := metadata.GetFile(ctx, in.FileID)
	if err != nil {
		return nil, 0, err
	}
	if !file.CreatedBy.Valid || file.CreatedBy.UUID != actor.UserID {
		return nil, 0, mediaModel.ErrFileNotFound.Err()
	}
	reader, _, err := u.archiveSources.OpenFileAt(ctx, in.FileID)
	return reader, file.SizeBytes, err
}

func previewChoices(tasks []ParsedExerciseArchive, in PreviewArchiveInput) (map[string]ImportTarget, error) {
	choices := map[string]ImportTarget{}
	known := map[string]bool{}
	used := map[uuid.UUID]bool{}
	for _, task := range tasks {
		known[task.ItemKey] = true
	}
	if in.FixedExerciseID != uuid.Nil && len(tasks) != 1 {
		return nil, exerciseModel.ErrImportFixedTargetInvalid.WithPublicContext("field", "target").Err()
	}
	if in.Targets != nil {
		for _, choice := range in.Targets {
			if !known[choice.ItemKey] || choices[choice.ItemKey].ItemKey != "" || (choice.Mode != "new" && choice.Mode != "update") || (choice.Mode == "new" && choice.ExerciseID != uuid.Nil) || (choice.Mode == "update" && choice.ExerciseID == uuid.Nil) {
				return nil, importItemError(exerciseModel.ErrImportSelectionInvalid.Err(), choice.ItemKey, "target")
			}
			if choice.Mode == "update" && used[choice.ExerciseID] {
				return nil, importItemError(exerciseModel.ErrImportTargetDuplicate.Err(), choice.ItemKey, "target")
			}
			if choice.Mode == "update" {
				used[choice.ExerciseID] = true
			}
			choices[choice.ItemKey] = choice
		}
		for _, task := range tasks {
			if _, ok := choices[task.ItemKey]; !ok {
				return nil, importItemError(exerciseModel.ErrImportSelectionInvalid.Err(), task.ItemKey, "target")
			}
		}
	}
	if in.FixedExerciseID != uuid.Nil {
		key := tasks[0].ItemKey
		if choice, ok := choices[key]; ok && (choice.Mode != "update" || choice.ExerciseID != in.FixedExerciseID) {
			return nil, importItemError(exerciseModel.ErrImportFixedTargetInvalid.Err(), key, "target")
		}
		choices[key] = ImportTarget{ItemKey: key, Mode: "update", ExerciseID: in.FixedExerciseID}
	}
	return choices, nil
}

func (u *ExerciseUseCase) previewTarget(ctx context.Context, actor Actor, id uuid.UUID) (exerciseModel.Exercise, exerciseModel.ExerciseVersion, error) {
	empty := exerciseModel.ExerciseVersion{}
	if _, err := u.AuthorizeExercise(ctx, actor, id, ActionWrite); err != nil {
		return exerciseModel.Exercise{}, empty, err
	}
	e, err := u.exercises.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return e, empty, exerciseModel.ErrExerciseNotFound.Err()
		}
		return e, empty, model.ErrPlatform.WithError(err).WithMessage("Failed to read import target").Err()
	}
	if err = e.EnsureNotArchived(); err != nil {
		return e, empty, err
	}
	v := exerciseModel.ExerciseVersion{Name: e.Name, Description: e.Description, Tags: e.Tags}
	if e.DraftVersionID.Valid {
		v, err = u.exercises.GetDraft(ctx, e.ID)
	} else if e.PublishedVersionID.Valid {
		v, err = u.exercises.GetVersion(ctx, e.PublishedVersionID.UUID)
	}
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return e, empty, exerciseModel.ErrExerciseModified.Err()
		}
		return e, empty, model.ErrPlatform.WithError(err).WithMessage("Failed to read import target version").Err()
	}
	if (v.ExerciseID != uuid.Nil && v.ExerciseID != e.ID) || (e.DraftVersionID.Valid && v.ID != e.DraftVersionID.UUID) {
		return e, empty, exerciseModel.ErrExerciseModified.Err()
	}
	// A Publish can move the selected row to history between the parent and
	// version reads. Refuse that mixed view rather than confirming an old slot.
	if (e.DraftVersionID.Valid && !v.Status.IsDraft()) || (!e.DraftVersionID.Valid && e.PublishedVersionID.Valid && !v.Status.IsPublished()) {
		return e, empty, exerciseModel.ErrExerciseModified.Err()
	}
	return e, v, nil
}

func importItemError(failure error, key, field string) error {
	var creator appErr.ErrorCreator
	if !errors.As(failure, &creator) {
		return failure
	}
	creator = creator.WithPublicContext("itemKey", key)
	if field != "" {
		creator = creator.WithPublicContext("field", field)
	}
	return creator.Err()
}

func (u *ExerciseUseCase) inspectImportImages(ctx context.Context, tasks []ParsedExerciseArchive) (map[string]imageRegistryUseCase.ArchiveImageInspection, map[string]string, error) {
	images := []ParsedArchiveImage{}
	seen := map[string]bool{}
	for _, task := range tasks {
		for _, image := range task.Images {
			if !seen[image.Marker] {
				seen[image.Marker] = true
				images = append(images, image)
			}
		}
	}
	result := map[string]imageRegistryUseCase.ArchiveImageInspection{}
	hashes := map[string]string{}
	if len(images) == 0 {
		return result, hashes, nil
	}
	inspector, ok := u.archiveImages.(archiveImageInspector)
	if !ok {
		return nil, nil, model.ErrPlatform.WithMessage("Image inspection is not wired").Err()
	}
	if err := u.archiveImages.RequireImageUploads(ctx); err != nil {
		return nil, nil, err
	}
	indexed := make([]*ociregistry.ImageArchive, len(images))
	for i, image := range images {
		archive, err := u.archiveImages.IndexImageArchive(image.file.open, image.file.Size)
		if err != nil {
			return nil, nil, withImageFile(err, image.file.Name)
		}
		if image.file.checksum != "" && archive.SHA256 != image.file.checksum {
			return nil, nil, archiveChecksumMismatch(image.file.Name)
		}
		indexed[i] = archive
		hashes[image.file.Name] = archive.SHA256
	}
	for i, image := range images {
		inspected, err := inspector.InspectImageArchive(ctx, indexed[i], image.Device)
		if err != nil {
			return nil, nil, withImageFile(err, image.file.Name)
		}
		result[image.Marker] = inspected
	}
	return result, hashes, nil
}

func previewSourceFingerprint(read archiveRead, imageHashes map[string]string) string {
	h := sha256.New()
	// JSON encodes names and lengths unambiguously; sorted entries make map order irrelevant.
	for _, name := range sortedArchiveNames(read.files) {
		body := read.files[name]
		encoded, _ := json.Marshal([]any{name, len(body)})
		_, _ = h.Write(encoded)
		_, _ = h.Write(body)
	}
	encoded, _ := json.Marshal(imageHashes)
	_, _ = h.Write(encoded)
	return hex.EncodeToString(h.Sum(nil))
}

func previewRevision(in PreviewArchiveInput, sourceHash, key string, position int, target ImportTarget, e exerciseModel.Exercise, v exerciseModel.ExerciseVersion) string {
	data, _ := json.Marshal(struct {
		UploadID, FileID    uuid.UUID
		SourceHash, ItemKey string
		Position            int
		Target              ImportTarget
		OwnerEventID        *uuid.UUID
		Exercise            exerciseModel.Exercise
		Version             exerciseModel.ExerciseVersion
	}{in.UploadID, in.FileID, sourceHash, key, position, target, in.OwnerEventID, e, v})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
