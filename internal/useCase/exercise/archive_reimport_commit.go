package exercise

import (
	"bytes"
	"context"
	"errors"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	"slices"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/gofrs/uuid"
)

var errStaleImportAttempt = errors.New("stale import attempt")

// ImportRawArchive is the legacy all-NEW multipart path. An UPDATE must use an
// immutable source-backed route so its confirmation binds the same source.
func (u *ExerciseUseCase) ImportRawArchive(ctx context.Context, actor Actor, in ImportExerciseInput) (ArchiveImportStart, error) {
	if err := u.AuthorizeImport(ctx, actor, in.OwnerEventID); err != nil {
		return ArchiveImportStart{}, err
	}
	lim := in.Limits
	if lim == (exerciseModel.ArchiveLimits{}) {
		lim = u.ArchiveLimits(ctx)
	}
	entries, protected, err := openZipAt(bytes.NewReader(in.Archive), int64(len(in.Archive)), in.Password, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	read, err := readArchiveEntries(entries, protected, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	return u.commitArchiveRead(ctx, actor, CommitArchiveInput{PreviewArchiveInput: PreviewArchiveInput{Password: in.Password, OwnerEventID: in.OwnerEventID}}, read, nil, nil)
}

// CommitArchiveImport applies explicit choices to the same owned immutable source
// used for preview. Omitted choices deliberately mean NEW, never auto-update.
func (u *ExerciseUseCase) CommitArchiveImport(ctx context.Context, actor Actor, in CommitArchiveInput) (ArchiveImportStart, error) {
	if id, found, err := u.replayArchiveImport(ctx, actor, in); err != nil || found {
		return ArchiveImportStart{JobID: id}, err
	}
	if err := u.RequireAuthor(ctx, actor); err != nil {
		return ArchiveImportStart{}, err
	}
	r, size, err := u.previewArchiveSource(ctx, actor, in.PreviewArchiveInput)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	lim := u.ArchiveLimits(ctx)
	if err = checkArchiveSize(size, lim.WithImagesBytes()); err != nil {
		return ArchiveImportStart{}, err
	}
	entries, protected, err := openZipAt(r, size, in.Password, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	read, err := readArchiveEntries(entries, protected, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	if len(read.images) > 0 {
		if _, err = u.prepareArchiveCommit(ctx, actor, &in, read); err != nil {
			return ArchiveImportStart{}, err
		}
		source, id := exerciseModel.ImportSourceUpload, in.UploadID
		if id == uuid.Nil {
			source, id = exerciseModel.ImportSourceFile, in.FileID
		}
		jobID, err := u.QueueArchiveImport(ctx, QueueImportInput{Source: source, SourceID: id, Password: in.Password, CreatedBy: actor.UserID, OwnerEventID: in.OwnerEventID, Options: ImportOptions{Targets: in.Targets, FixedExerciseID: in.FixedExerciseID, ConfirmedUpdates: in.ConfirmedUpdates}})
		return ArchiveImportStart{JobID: jobID}, err
	}
	result, err := u.commitArchiveRead(ctx, actor, in, read, nil, nil)
	if err == nil && in.UploadID != uuid.Nil {
		u.dropArchiveUpload(context.WithoutCancel(ctx), in.UploadID)
	}
	return result, err
}

func (u *ExerciseUseCase) currentImportActor(ctx context.Context, q interface {
	GetUserByID(context.Context, uuid.UUID) (postgres.User, error)
}, id uuid.UUID) (Actor, error) {
	row, err := q.GetUserByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return Actor{}, exerciseModel.ErrExerciseForbidden.Err()
		}
		return Actor{}, err
	}
	if row.Status == string(userModel.UserStatusBlocked) || row.Status == string(userModel.UserStatusDeleted) {
		return Actor{}, exerciseModel.ErrExerciseForbidden.Err()
	}
	return Actor{UserID: id, Role: rbac.Role(row.Role)}, nil
}

// confirmImportTargets performs no writes and is repeated under sorted parent
// locks after uploads. Its revision binds source bytes/snapshot + target state.
func (u *ExerciseUseCase) confirmImportTargets(ctx context.Context, actor Actor, in CommitArchiveInput, tasks []ParsedExerciseArchive, choices map[string]ImportTarget, sourceHash string) error {
	confirmed := map[string]ConfirmedImportUpdate{}
	for _, c := range in.ConfirmedUpdates {
		if c.ItemKey == "" || confirmed[c.ItemKey].ItemKey != "" || choices[c.ItemKey].Mode != "update" {
			return importItemError(exerciseModel.ErrImportConfirmationRequired.Err(), c.ItemKey, "confirmation")
		}
		confirmed[c.ItemKey] = c
	}
	for _, task := range tasks {
		choice := choices[task.ItemKey]
		if choice.Mode == "new" {
			if err := u.AuthorizeImport(ctx, actor, in.OwnerEventID); err != nil {
				return importItemError(err, task.ItemKey, "target")
			}
			if err := u.requireImportedPublishable(ctx, task.Versions, in.OwnerEventID); err != nil {
				return importItemError(err, task.ItemKey, "")
			}
			continue
		}
		e, v, err := u.previewTarget(ctx, actor, choice.ExerciseID)
		if err != nil {
			return importItemError(err, task.ItemKey, "target")
		}
		selected := workingImportVersion(task.Versions)
		source := task.Versions[selected]
		if err = u.requireInfrastructureAllowed(ctx, e, source.Variants); err != nil {
			return importItemError(err, task.ItemKey, "target")
		}
		c := confirmed[task.ItemKey]
		if c.Revision == "" || (e.DraftVersionID.Valid && !c.ReplaceDraft) {
			return importItemError(exerciseModel.ErrImportConfirmationRequired.Err(), task.ItemKey, "confirmation")
		}
		if c.Revision != previewRevision(in.PreviewArchiveInput, sourceHash, task.ItemKey, selected, choice, e, v) {
			return importItemError(exerciseModel.ErrImportConfirmationStale.Err(), task.ItemKey, "confirmation")
		}
	}
	return nil
}

type preparedArchiveCommit struct {
	tasks      []ParsedExerciseArchive
	choices    map[string]ImportTarget
	sourceHash string
}

func (u *ExerciseUseCase) prepareArchiveCommit(ctx context.Context, actor Actor, in *CommitArchiveInput, read archiveRead) (*preparedArchiveCommit, error) {
	tasks, err := parseArchiveRead(read)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if err = u.validateImportContent(&tasks[i], read.protected); err != nil {
			return nil, importItemError(err, tasks[i].ItemKey, "")
		}
		for vi := range tasks[i].Versions {
			v := &tasks[i].Versions[vi]
			if v.Status.IsPublished() {
				if err = v.ValidateForPublish(); err != nil {
					return nil, importItemError(withImportedPath(err, vi, v.Variants), tasks[i].ItemKey, "")
				}
			}
		}
	}
	choices, err := previewChoices(tasks, in.PreviewArchiveInput)
	if err != nil {
		return nil, err
	}
	if in.Targets == nil && in.FixedExerciseID == uuid.Nil {
		// Preserve the omitted-choice plan for existing queued all-NEW jobs.
		// Suggestions are never promoted into an implicit UPDATE at commit.
		for _, task := range tasks {
			choices[task.ItemKey] = ImportTarget{ItemKey: task.ItemKey, Mode: "new"}
		}
	}
	_, hashes, err := u.inspectImportImages(ctx, tasks)
	if err != nil {
		return nil, err
	}
	sourceHash := previewSourceFingerprint(read, hashes)
	if err = u.confirmImportTargets(ctx, actor, *in, tasks, choices, sourceHash); err != nil {
		return nil, err
	}
	return &preparedArchiveCommit{tasks: tasks, choices: choices, sourceHash: sourceHash}, nil
}

func (u *ExerciseUseCase) commitArchiveRead(ctx context.Context, actor Actor, in CommitArchiveInput, read archiveRead, job *exerciseModel.ImportJob, progress *importProgress) (ArchiveImportStart, error) {
	prepared, err := u.prepareArchiveCommit(ctx, actor, &in, read)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	tasks, choices, sourceHash := prepared.tasks, prepared.choices, prepared.sourceHash
	progress.tasksTotal(ctx, len(tasks))
	// All archive/policy/confirmation checks precede external staging. The final
	// check below uses these original snapshots before generated IDs are stored.
	refs, err := u.pushArchiveImages(ctx, tasks, actor.UserID, progress)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	// Keep the unmodified prepared source for exact revision recomputation; the
	// stage copies are parsed independently to avoid aliasing IDs/ciphertext.
	staged, err := parseArchiveRead(read)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	for i := range staged {
		if err = u.validateImportContent(&staged[i], read.protected); err != nil {
			return ArchiveImportStart{}, err
		}
		if choices[staged[i].ItemKey].Mode == "update" {
			selected := workingImportVersion(staged[i].Versions)
			staged[i].Versions = []exerciseModel.ExerciseVersion{staged[i].Versions[selected]}
		}
		if err = u.encryptImportedSecrets(staged[i].Versions); err != nil {
			return ArchiveImportStart{}, err
		}
		if err = replaceImageMarkers(staged[i].Versions, refs); err != nil {
			return ArchiveImportStart{}, err
		}
		if err = u.copyImportedAttachments(ctx, &staged[i], actor.UserID); err != nil {
			return ArchiveImportStart{}, err
		}
	}
	progress.stage(ctx, exerciseModel.ImportStageTasks)
	ctx, session, q, unit, err := u.beginCatalog(ctx)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	defer unit.Restore()
	if job != nil {
		row, loadErr := q.LockExerciseImportJob(ctx, job.ID)
		if loadErr != nil {
			return ArchiveImportStart{}, loadErr
		}
		if row.Generation != job.Generation || row.State != string(exerciseModel.ImportJobRunning) || row.PlanHash != job.PlanHash {
			return ArchiveImportStart{}, errStaleImportAttempt
		}
	}
	ids := []uuid.UUID{}
	for _, choice := range choices {
		if choice.Mode == "update" {
			ids = append(ids, choice.ExerciseID)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	for _, id := range ids {
		if _, err = q.LockExerciseForImport(ctx, id); err != nil {
			if !repositoryTools.IsObjectNotFoundError(err) {
				return ArchiveImportStart{}, err
			}
			for key, choice := range choices {
				if choice.ExerciseID == id {
					return ArchiveImportStart{}, importItemError(exerciseModel.ErrExerciseNotFound.Err(), key, "target")
				}
			}
		}
	}
	actor, err = session.currentImportActor(ctx, q, actor.UserID)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	if err = session.RequireAuthor(ctx, actor); err != nil {
		return ArchiveImportStart{}, err
	}
	if err = session.lockArchiveImportSource(ctx, q, actor, in.PreviewArchiveInput); err != nil {
		return ArchiveImportStart{}, err
	}
	if err = session.confirmImportTargets(ctx, actor, in, tasks, choices, sourceHash); err != nil {
		return ArchiveImportStart{}, err
	}
	result := ArchiveImportStart{Exercises: []ExerciseView{}, Results: []exerciseModel.ImportResult{}}
	for i := range staged {
		parsed := &staged[i]
		choice := choices[parsed.ItemKey]
		var view ExerciseView
		mode := choice.Mode
		if choice.Mode == "new" {
			view, err = session.storeStagedNewImport(ctx, q, parsed, actor.UserID, in.OwnerEventID, true)
			result.Created++
		} else {
			var unchanged bool
			view, unchanged, err = session.unchangedImport(ctx, actor, choice.ExerciseID, tasks, parsed.ItemKey, refs)
			if err == nil && unchanged {
				// The archive brings nothing new: the working copy stays as it is (no new draft, no lost edits).
				mode = ImportModeUnchanged
				result.Unchanged++
			} else if err == nil {
				view, err = session.storeStagedUpdate(ctx, choice.ExerciseID, parsed.Versions[0], actor.UserID)
				result.Updated++
			}
		}
		if err != nil {
			return ArchiveImportStart{}, importItemError(err, parsed.ItemKey, "")
		}
		result.Exercises = append(result.Exercises, view)
		result.Results = append(result.Results, exerciseModel.ImportResult{ItemKey: parsed.ItemKey, Mode: mode, ExerciseID: view.ID})
	}
	if job != nil {
		done := *job
		done.Results = result.Results
		ids = make([]uuid.UUID, len(result.Results))
		for i, item := range result.Results {
			ids[i] = item.ExerciseID
		}
		done.Finish(ids, u.timeNow())
		if progress != nil {
			done.Progress = progress.job.Progress
			done.Finish(ids, u.timeNow())
		}
		n, saveErr := session.exercises.SaveImportJob(ctx, done)
		if saveErr != nil {
			return ArchiveImportStart{}, saveErr
		}
		if n != 1 {
			return ArchiveImportStart{}, errStaleImportAttempt
		}
	}
	if err = unit.Save(); err != nil {
		return ArchiveImportStart{}, err
	}
	return result, nil
}

func (u *ExerciseUseCase) storeStagedUpdate(ctx context.Context, id uuid.UUID, source exerciseModel.ExerciseVersion, by uuid.UUID) (ExerciseView, error) {
	e, err := u.loadEditable(ctx, id)
	if err != nil {
		return ExerciseView{}, err
	}
	if err = u.requireFilesClaimable(ctx, id, by, exerciseModel.CollectFileIDs(source.Variants)); err != nil {
		return ExerciseView{}, err
	}
	if err = u.adoptFiles(ctx, id, source.Variants); err != nil {
		return ExerciseView{}, err
	}
	saved, err := u.exercises.UpsertDraft(ctx, id, freshID(), source, u.timeNow(), uuid.NullUUID{UUID: by, Valid: by != uuid.Nil})
	if err != nil {
		return ExerciseView{}, err
	}
	metadata := e.WithVersionMetadata(source)
	metadata.UpdatedAt = u.timeNow()
	metadata.UpdatedBy = uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
	saved, err = u.exercises.SaveDraftIdentity(ctx, metadata, e.UpdatedAt, freshID())
	if err != nil {
		return ExerciseView{}, classifyExerciseWriteError(err, "import draft metadata")
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, saved.ID, exerciseModel.CollectFileIDs(saved.Variants)); err != nil {
		return ExerciseView{}, err
	}
	e, err = u.exercises.GetByID(ctx, id)
	if err != nil {
		return ExerciseView{}, err
	}
	return u.exerciseView(ctx, e.WithVersionMetadata(saved))
}

func (u *ExerciseUseCase) storeStagedNewImport(ctx context.Context, q CatalogRepository, parsed *ParsedExerciseArchive, by uuid.UUID, eventID *uuid.UUID, refresh bool) (ExerciseView, error) {
	source := parsed.Exercise
	selected := workingImportVersion(parsed.Versions)
	for i := range parsed.Versions {
		if parsed.Versions[i].Status.IsPublished() {
			selected = i
			break
		}
	}
	if selected >= 0 {
		source = source.WithVersionMetadata(parsed.Versions[selected])
	}
	var created exerciseModel.Exercise
	for attempt := 0; attempt < 100; attempt++ {
		name := importedName(source.Name, attempt)
		var entity exerciseModel.Exercise
		var err error
		if eventID == nil {
			entity, err = exerciseModel.NewExercise(name, source.Description, source.Tags, by, time.Now())
		} else {
			entity, err = exerciseModel.NewEventExercise(name, source.Description, source.Tags, *eventID, by, time.Now())
		}
		if err != nil {
			return ExerciseView{}, err
		}
		row, err := q.TryCreateImportedExercise(ctx, postgres.TryCreateImportedExerciseParams{ID: entity.ID, Name: entity.Name, Description: entity.Description, Tags: entity.Tags, CreatedAt: entity.CreatedAt, CreatedBy: entity.CreatedBy, UpdatedAt: entity.UpdatedAt, UpdatedBy: entity.UpdatedBy, Scope: int16(entity.Scope), OwnerEventID: entity.OwnerEventID, AccessLevel: int16(entity.AccessLevel)})
		if repositoryTools.IsObjectNotFoundError(err) {
			continue
		}
		if err != nil {
			return ExerciseView{}, err
		}
		created = exerciseRepo.ToDomain(row)
		break
	}
	if created.ID == uuid.Nil {
		return ExerciseView{}, model.ErrPlatform.WithMessage("Could not allocate an imported exercise name").Err()
	}
	if created.Name != source.Name {
		for i := range parsed.Versions {
			v := &parsed.Versions[i]
			if (v.Status.IsDraft() || v.Status.IsPublished()) && v.Name == source.Name {
				v.Name = created.Name
			}
		}
	}
	if err := u.adoptImportedFiles(ctx, created.ID, parsed.Versions); err != nil {
		return ExerciseView{}, err
	}
	if err := u.persistImportedVersions(ctx, created.ID, parsed.Versions, by); err != nil {
		return ExerciseView{}, err
	}
	if !refresh {
		return u.exerciseView(ctx, created)
	}
	created, err := u.exercises.GetByID(ctx, created.ID)
	if err != nil {
		return ExerciseView{}, err
	}
	return u.exerciseView(ctx, created)
}

// A live/done owned job is a durable outcome path, not a new execution. Compare
// the exact canonical non-secret plan before reading an old/consumed source or
// rejecting the confirmation that the completed update necessarily invalidated.
func (u *ExerciseUseCase) replayArchiveImport(ctx context.Context, actor Actor, in CommitArchiveInput) (uuid.UUID, bool, error) {
	if (in.UploadID == uuid.Nil) == (in.FileID == uuid.Nil) {
		return uuid.Nil, false, exerciseModel.ErrImportSourceInvalid.Err()
	}
	source, id := exerciseModel.ImportSourceUpload, in.UploadID
	if id == uuid.Nil {
		source, id = exerciseModel.ImportSourceFile, in.FileID
	}
	job, err := u.exercises.GetOwnedImportJobBySource(ctx, source, id, actor.UserID)
	if repositoryTools.IsObjectNotFoundError(err) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, model.ErrPlatform.WithError(err).WithMessage("Failed to read the import").Err()
	}
	state, _ := job.Current(u.timeNow())
	if state == exerciseModel.ImportJobFailed {
		return uuid.Nil, false, nil
	}
	_, plan, err := canonicalImportPlan(ImportOptions{Targets: in.Targets, FixedExerciseID: in.FixedExerciseID, ConfirmedUpdates: in.ConfirmedUpdates}, in.OwnerEventID)
	if err != nil {
		return uuid.Nil, false, err
	}
	existing := job.PlanHash
	if existing == "" {
		_, existing, err = canonicalImportPlan(ImportOptions{}, uuidPtr(job.OwnerEventID))
		if err != nil {
			return uuid.Nil, false, err
		}
	}
	if plan != existing {
		return uuid.Nil, false, exerciseModel.ErrImportPlanConflict.Err()
	}
	return job.ID, true, nil
}

// The database metadata is the final authority after external staging and any
// target-lock wait. The row lock prevents deletion/consumption/eligibility
// changes until tasks, references and a durable success outcome commit.
func (u *ExerciseUseCase) lockArchiveImportSource(ctx context.Context, q CatalogRepository, actor Actor, in PreviewArchiveInput) error {
	if in.UploadID != uuid.Nil {
		row, err := q.LockExerciseImportUpload(ctx, in.UploadID)
		if repositoryTools.IsObjectNotFoundError(err) {
			return mediaModel.ErrUploadNotFound.Err()
		}
		if err != nil {
			return err
		}
		upload := mediaModel.Upload{ID: row.ID, CreatedBy: row.CreatedBy, SizeBytes: row.SizeBytes, ChunkBytes: row.ChunkBytes, ChunksReceived: int(row.ChunksReceived), ExpiresAt: row.ExpiresAt, Purpose: mediaModel.UploadPurpose(row.Purpose)}
		if !upload.OwnedBy(actor.UserID) || upload.Expired(time.Now()) {
			return mediaModel.ErrUploadNotFound.Err()
		}
		if upload.Purpose != mediaModel.UploadPurposeExerciseArchive || !upload.Complete() {
			return exerciseModel.ErrArchiveUploadNotReady.Err()
		}
	} else if in.FileID != uuid.Nil {
		row, err := q.LockExerciseImportFile(ctx, in.FileID)
		if repositoryTools.IsObjectNotFoundError(err) {
			return mediaModel.ErrFileNotFound.Err()
		}
		if err != nil {
			return err
		}
		if !row.CreatedBy.Valid || row.CreatedBy.UUID != actor.UserID {
			return mediaModel.ErrFileNotFound.Err()
		}
	}
	return nil // Trusted legacy/raw NEW helpers have no persisted source ID.
}

// ImportModeUnchanged marks an update the import skipped: the target's working copy already had that content.
const ImportModeUnchanged = "unchanged"

// unchangedImport tells whether an archive item brings nothing new to its target: the same general data, variants,
// subtasks, flags, files and images as the target's working copy (compared like the preview's change summary, the
// images by the digests the import pushed). It also returns the target's view for the result.
func (u *ExerciseUseCase) unchangedImport(ctx context.Context, actor Actor, target uuid.UUID, tasks []ParsedExerciseArchive, itemKey string, refs map[string]string) (ExerciseView, bool, error) {
	index := slices.IndexFunc(tasks, func(task ParsedExerciseArchive) bool { return task.ItemKey == itemKey })
	if index < 0 {
		return ExerciseView{}, false, nil
	}
	task := tasks[index]
	selected := workingImportVersion(task.Versions)
	if selected < 0 {
		return ExerciseView{}, false, nil
	}
	e, current, err := u.previewTarget(ctx, actor, target)
	if err != nil {
		return ExerciseView{}, false, err
	}
	images := map[string]imageRegistryUseCase.ArchiveImageInspection{}
	for marker, ref := range refs {
		if parsed, ok := imageRegistryModel.ParsePlatformRef(ref); ok {
			images[marker] = imageRegistryUseCase.ArchiveImageInspection{Reference: ref, Digest: parsed.Digest}
		}
	}
	summary, err := u.importSummary(ctx, task, task.Versions[selected], current, images)
	if err != nil {
		return ExerciseView{}, false, err
	}
	if !summary.Unchanged() {
		return ExerciseView{}, false, nil
	}
	view, err := u.exerciseView(ctx, e.WithVersionMetadata(current))
	return view, err == nil, err
}
