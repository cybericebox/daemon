package exercise

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	appErr "github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Background import of task archives that name device image files. Such an import pushes up to 10 GiB into the
// registry, so it never runs inside a request: the request records an import job (one per archive source) and
// queues it; the job (RunArchiveImport) reads the archive where it lies and records done (the created tasks) or
// failed (the error the user sees); ArchiveImportJob reads it, for the user who started it only. Archives without
// image files keep the synchronous import.
//
// The archive password (an archive with secrets) is sealed with the platform cipher, bound to the job id, into the
// River job's arguments; it is opened only by the job and never stored in plain text.

type (
	// IArchiveImages indexes and pushes the image files of a task archive (the image registry use case).
	IArchiveImages interface {
		RequireImageUploads(ctx context.Context) error
		IndexImageArchive(open ociregistry.ArchiveSource, size int64) (*ociregistry.ImageArchive, error)
		PushImageArchive(ctx context.Context, in imageRegistryUseCase.PushArchiveInput) (imageRegistryUseCase.CreateImageResult, error)
	}

	// IArchiveSources reads an archive where it lies (the media use case).
	IArchiveSources interface {
		UploadStatus(ctx context.Context, id, owner uuid.UUID) (mediaModel.Upload, error)
		OpenUploadAt(ctx context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.Upload, error)
		OpenFileAt(ctx context.Context, id uuid.UUID) (io.ReaderAt, mediaModel.File, error)
		DropUpload(ctx context.Context, id uuid.UUID) error
	}

	// ImportJobQueue queues the background import (unique per job while queued or running).
	ImportJobQueue func(ctx context.Context, args jobsModel.ExerciseImportArgs) error

	// ArchiveImportDependencies wire the background import; any of them nil: archives with image files are refused
	// as a platform error (the synchronous import still works).
	ArchiveImportDependencies struct {
		Images  IArchiveImages
		Sources IArchiveSources
		Queue   ImportJobQueue
		// Sealer seals the archive password into the job (PLATFORM_SECRETS_KEY).
		Sealer secret.Sealer
	}

	// ImportUploadInput names a complete chunked upload (purpose exercise-archive) of the caller.
	ImportUploadInput struct {
		UploadID  uuid.UUID
		Password  string
		CreatedBy uuid.UUID
		// OwnerEventID: nil imports into the platform catalog, else into that event (AuthorizeImport first).
		OwnerEventID *uuid.UUID
	}

	// QueueImportInput names the archive source of a background import.
	QueueImportInput struct {
		Source    exerciseModel.ImportSource
		SourceID  uuid.UUID
		Password  string
		CreatedBy uuid.UUID
		// OwnerEventID: nil imports into the platform catalog, else into that event (AuthorizeImport first).
		OwnerEventID *uuid.UUID
		Options      ImportOptions
	}

	// ArchiveImportStart is either the tasks created at once (no image files) or the background import's job.
	ArchiveImportStart struct {
		Exercises []ExerciseView
		JobID     uuid.UUID
		Results   []exerciseModel.ImportResult
		Created   int
		Updated   int
	}

	// ImportJobView is an import job as its user sees it.
	ImportJobView struct {
		ID    uuid.UUID
		State exerciseModel.ImportJobState
		// Stage is unpacking, images or tasks while running, then done or failed; Progress holds the counts.
		Stage       exerciseModel.ImportStage
		Progress    exerciseModel.ImportProgress
		ExerciseIDs []uuid.UUID
		Results     []exerciseModel.ImportResult
		Created     int
		Updated     int
		Error       *exerciseModel.ImportJobError
	}
)

// SetArchiveImport wires the background import of archives with image files.
func (u *ExerciseUseCase) SetArchiveImport(deps ArchiveImportDependencies) {
	u.archiveImages, u.archiveSources, u.importQueue, u.importSealer = deps.Images, deps.Sources, deps.Queue, deps.Sealer
}

// passwordContext binds a sealed archive password to its job.
func passwordContext(jobID uuid.UUID) []byte {
	return []byte("exercise-import:password:" + jobID.String())
}

// ImportUploadedArchive imports a task archive uploaded in chunks (purpose exercise-archive) of the caller. An
// archive naming no image file is imported at once (the archive limits apply; the upload is then dropped); one
// naming image files is queued as a background import (JobID).
func (u *ExerciseUseCase) ImportUploadedArchive(ctx context.Context, in ImportUploadInput) (ArchiveImportStart, error) {
	if u.archiveSources == nil {
		return ArchiveImportStart{}, model.ErrPlatform.WithMessage("Archive import is not wired").Err()
	}
	upload, err := u.archiveSources.UploadStatus(ctx, in.UploadID, in.CreatedBy)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	if upload.Purpose != mediaModel.UploadPurposeExerciseArchive || !upload.Complete() {
		return ArchiveImportStart{}, exerciseModel.ErrArchiveUploadNotReady.Err()
	}
	reader, _, err := u.archiveSources.OpenUploadAt(ctx, in.UploadID)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	// One set of limits for the whole import.
	lim := u.ArchiveLimits(ctx)
	entries, protected, err := openZipAt(reader, upload.SizeBytes, in.Password, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	named, err := namesImageFiles(entries, protected, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	if named {
		jobID, queueErr := u.QueueArchiveImport(ctx, QueueImportInput{
			Source: exerciseModel.ImportSourceUpload, SourceID: in.UploadID, Password: in.Password, CreatedBy: in.CreatedBy,
			OwnerEventID: in.OwnerEventID,
		})
		if queueErr != nil {
			return ArchiveImportStart{}, queueErr
		}
		return ArchiveImportStart{JobID: jobID}, nil
	}
	// Without image files the archive limits are the synchronous import's.
	if err = checkArchiveSize(upload.SizeBytes, lim.ArchiveBytes); err != nil {
		return ArchiveImportStart{}, err
	}
	contents, err := ReadArchiveBytes(io.NewSectionReader(reader, 0, upload.SizeBytes), lim.ArchiveBytes)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	created, err := u.importArchiveBundle(ctx, contents, in.Password, in.CreatedBy, in.OwnerEventID, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	u.dropArchiveUpload(ctx, in.UploadID)
	return ArchiveImportStart{Exercises: created}, nil
}

// ImportArchiveBundle imports every task of an archive held in memory (one task folder or several), each as its
// own import: the synchronous path of archives without image files. The limits in force are read once for all.
func (u *ExerciseUseCase) ImportArchiveBundle(ctx context.Context, contents []byte, password string, createdBy uuid.UUID) ([]ExerciseView, error) {
	return u.importArchiveBundle(ctx, contents, password, createdBy, nil, u.ArchiveLimits(ctx))
}

func (u *ExerciseUseCase) importArchiveBundle(ctx context.Context, contents []byte, password string, createdBy uuid.UUID, ownerEventID *uuid.UUID, lim exerciseModel.ArchiveLimits) ([]ExerciseView, error) {
	entries, protected, err := openZipAt(bytes.NewReader(contents), int64(len(contents)), password, lim)
	if err != nil {
		return nil, err
	}
	read, err := readArchiveEntries(entries, protected, lim)
	if err != nil {
		return nil, err
	}
	if len(read.images) > 0 {
		return nil, exerciseModel.ErrArchiveImageFileUnsupported.Err()
	}
	tasks, err := parseArchiveRead(read)
	if err != nil {
		return nil, err
	}
	// Trusted legacy Go entry point: retain its caller-owned authorization contract,
	// but validate every NEW item before staging and commit the whole bundle once.
	for i := range tasks {
		if err = u.prepareImport(ctx, &tasks[i], protected, ownerEventID); err != nil {
			return nil, err
		}
	}
	for i := range tasks {
		if err = u.copyImportedAttachments(ctx, &tasks[i], createdBy); err != nil {
			return nil, err
		}
	}
	ctx, session, q, unit, err := u.beginCatalog(ctx)
	if err != nil {
		return nil, err
	}
	defer unit.Restore()
	views := make([]ExerciseView, 0, len(tasks))
	for i := range tasks {
		view, saveErr := session.storeStagedNewImport(ctx, q, &tasks[i], createdBy, ownerEventID, false)
		if saveErr != nil {
			return nil, saveErr
		}
		views = append(views, view)
	}
	if err = unit.Save(); err != nil {
		return nil, err
	}
	return views, nil

}

// QueueArchiveImport records the background import of an archive source and queues it; it returns the job id.
// One import per source: a running or done import of it is returned as it is, a failed one runs again. The
// registry not configured: the upload refusal, nothing recorded. The queued job checks the archive against the
// limits in force when it runs (RunArchiveImport reads them once for the whole import), not those of this request.
func (u *ExerciseUseCase) QueueArchiveImport(ctx context.Context, in QueueImportInput) (uuid.UUID, error) {
	if u.archiveImages == nil || u.importQueue == nil {
		return uuid.Nil, model.ErrPlatform.WithMessage("Image import is not wired").Err()
	}
	if err := u.archiveImages.RequireImageUploads(ctx); err != nil {
		return uuid.Nil, err
	}
	if in.Password != "" && u.importSealer == nil {
		return uuid.Nil, model.ErrPlatform.WithMessage("The archive password cannot be sealed: secret storage is not configured").Err()
	}
	now := u.timeNow()
	newID := freshID()
	owner := uuid.NullUUID{}
	if in.OwnerEventID != nil {
		owner = uuid.NullUUID{UUID: *in.OwnerEventID, Valid: true}
	}
	newJob := exerciseModel.NewImportJob(newID, in.CreatedBy, in.SourceID, in.Source, owner, now)
	var err error
	newJob.Options, newJob.PlanHash, err = canonicalImportPlan(in.Options, in.OwnerEventID)
	if err != nil {
		return uuid.Nil, err
	}
	job, err := u.exercises.StartImportJob(ctx, newJob)
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to record the import").Err()
	}
	if !job.OwnedBy(in.CreatedBy) {
		return uuid.Nil, exerciseModel.ErrImportJobNotFound.Err()
	}
	if job.ID != newID { // the source already has an import
		existingPlan := job.PlanHash
		if existingPlan == "" {
			_, existingPlan, err = canonicalImportPlan(ImportOptions{}, uuidPtr(job.OwnerEventID))
			if err != nil {
				return uuid.Nil, err
			}
		}
		if state, _ := job.Current(now); state != exerciseModel.ImportJobFailed && existingPlan != newJob.PlanHash {
			return uuid.Nil, exerciseModel.ErrImportPlanConflict.Err()
		}
		expectedGeneration := attemptGeneration(job.Generation)
		job.Generation = expectedGeneration
		if !job.Restart(owner, now) {
			return job.ID, nil // running or done: the same import
		}
		job.Options, job.PlanHash = newJob.Options, newJob.PlanHash
		if job, err = u.exercises.RestartImportJob(ctx, job, expectedGeneration, now); err != nil {
			return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to record the import").Err()
		}
	}
	args := jobsModel.ExerciseImportArgs{JobID: job.ID, Generation: attemptGeneration(job.Generation)}
	// The sealed password stays in river_job.args (sealed, never plain) until River prunes the finished job.
	if in.Password != "" {
		if args.SealedPassword, err = u.importSealer.EncryptWithContext([]byte(in.Password), passwordContext(job.ID)); err != nil {
			return uuid.Nil, u.failQueued(ctx, job, model.ErrPlatform.WithError(err).WithMessage("Failed to seal the archive password").Err())
		}
	}
	if err = u.importQueue(ctx, args); err != nil {
		return uuid.Nil, u.failQueued(ctx, job, model.ErrPlatform.WithError(err).WithMessage("Failed to queue the import").Err())
	}
	return job.ID, nil
}

// failQueued records a job that could not be queued as failed (it may be started again) and returns the cause.
func (u *ExerciseUseCase) failQueued(ctx context.Context, job exerciseModel.ImportJob, cause error) error {
	job.Fail(importJobFailure(cause), u.timeNow())
	if err := u.writeImportJob(ctx, job); err != nil {
		log.Warn().Err(err).Str("job", job.ID.String()).Msg("archive import: recording the queue failure failed")
	}
	return cause
}

// ArchiveImportJob reads an import job of user; another user's job is not found, like an absent one.
func (u *ExerciseUseCase) ArchiveImportJob(ctx context.Context, id, user uuid.UUID) (ImportJobView, error) {
	job, err := u.exercises.GetImportJob(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ImportJobView{}, exerciseModel.ErrImportJobNotFound.WithError(err).Err()
		}
		return ImportJobView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the import").Err()
	}
	if !job.OwnedBy(user) {
		return ImportJobView{}, exerciseModel.ErrImportJobNotFound.Err()
	}
	state, failure := job.Current(u.timeNow())
	view := ImportJobView{ID: job.ID, State: state, Stage: job.Stage(u.timeNow()), Progress: job.Progress, Error: failure}
	if state == exerciseModel.ImportJobDone {
		view.ExerciseIDs = job.ExerciseIDs
		view.Results = job.Results
		for _, item := range job.Results {
			if item.Mode == "update" {
				view.Updated++
			} else {
				view.Created++
			}
		}
		if len(job.Results) == 0 {
			view.Created = len(job.ExerciseIDs)
		}
	}
	return view, nil
}

// RunningArchiveImports lists the imports of user that still run, so a page opened on another device (or with a
// cleared browser) can follow them. A running import silent for longer than its job may run has stopped and is left out.
func (u *ExerciseUseCase) RunningArchiveImports(ctx context.Context, user uuid.UUID) ([]ImportJobView, error) {
	jobs, err := u.exercises.ListRunningImportJobs(ctx, user, u.timeNow().Add(-exerciseModel.ImportJobStaleAfter))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list the imports").Err()
	}
	views := make([]ImportJobView, len(jobs))
	for i, job := range jobs {
		views[i] = ImportJobView{ID: job.ID, State: exerciseModel.ImportJobRunning, Stage: job.Stage(u.timeNow()), Progress: job.Progress}
	}
	return views, nil
}

// RunArchiveImport is the background import (River job exercise_import): it reads the archive source where it lies,
// imports it all or nothing and records done or failed. Only a failure to record the outcome is returned (River
// retries then). The outcome is written on a context of its own, so an import cut off by its timeout still ends
// recorded as failed.
func (u *ExerciseUseCase) RunArchiveImport(ctx context.Context, args jobsModel.ExerciseImportArgs) error {
	job, err := u.exercises.GetImportJob(ctx, args.JobID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil // the user is gone with the job: nothing to import
		}
		return err
	}
	job.Generation = attemptGeneration(job.Generation)
	if job.State != exerciseModel.ImportJobRunning || job.Generation != attemptGeneration(args.Generation) {
		return nil
	}
	progress := u.newImportProgress(job)
	result, runErr := u.runArchiveImport(ctx, job, args.SealedPassword, progress)
	job = progress.job // the outcome is written with how far the import came
	if runErr != nil {
		if errors.Is(runErr, errStaleImportAttempt) {
			return nil
		}
		// A commit acknowledgement may be lost after PostgreSQL durably commits
		// both tasks and done. Never turn that outcome into failure or reapply.
		if current, readErr := u.exercises.GetImportJob(ctx, job.ID); readErr == nil && attemptGeneration(current.Generation) == job.Generation && current.State == exerciseModel.ImportJobDone {
			return nil
		}
		log.Warn().Err(runErr).Str("job", job.ID.String()).Msg("archive import failed")
		failure := importJobFailure(runErr)
		if ctx.Err() != nil {
			// Cut off (timeout, shutdown): whatever the last call failed with, the import was stopped.
			failure = importJobFailure(exerciseModel.ErrArchiveImportStopped.WithError(runErr).Err())
		}
		job.Fail(failure, u.timeNow())
		// Nothing was created: if the failure cannot be recorded, River may run the import again.
		if err = u.writeImportJobRetrying(ctx, job); err != nil {
			if errors.Is(err, errStaleImportAttempt) {
				return nil
			}
			return err
		}
		u.announceImport(ctx, job, false, 0)
		return nil
	}
	if job.Source == exerciseModel.ImportSourceUpload {
		u.dropArchiveUpload(context.WithoutCancel(ctx), job.SourceID)
	}
	job.Results = result.Results
	u.announceImport(ctx, job, true, result.Created)
	return nil
}

// announceImport puts the end of the import into the inbox of the user who started it. Best effort: the outcome is
// already recorded, so a delivery failure is logged, not returned.
func (u *ExerciseUseCase) announceImport(ctx context.Context, job exerciseModel.ImportJob, done bool, created int) {
	if u.importInbox == nil {
		return
	}
	c := context.WithoutCancel(ctx)
	end := inboxUseCase.ImportEnd{User: job.CreatedBy, Created: created, At: u.timeNow()}
	for _, item := range job.Results {
		if item.Mode == "update" {
			end.Updated++
		}
	}
	var err error
	if done {
		err = u.importInbox.ExerciseImportDone(c, end)
	} else {
		err = u.importInbox.ExerciseImportFailed(c, end)
	}
	if err != nil {
		log.Warn().Err(err).Str("job", job.ID.String()).Msg("archive import: the inbox note failed")
	}
}

// importStateWrites is how often an import's outcome is written before giving up.
const importStateWrites = 5

// writeImportJobRetrying writes the job up to importStateWrites times, importStateRetryDelay apart; the wait ends
// early when ctx does (the last write's error is returned then).
func (u *ExerciseUseCase) writeImportJobRetrying(ctx context.Context, job exerciseModel.ImportJob) error {
	var err error
	for attempt := 0; attempt < importStateWrites; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(importStateRetryDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return err
			}
		}
		if err = u.writeImportJob(ctx, job); err == nil || errors.Is(err, errStaleImportAttempt) {
			return err
		}
	}
	return err
}

// runArchiveImport opens the source and the password and imports the archive.
func (u *ExerciseUseCase) runArchiveImport(ctx context.Context, job exerciseModel.ImportJob, sealedPassword string, progress *importProgress) (ArchiveImportStart, error) {
	if u.archiveSources == nil {
		return ArchiveImportStart{}, model.ErrPlatform.WithMessage("Archive import is not wired").Err()
	}
	password := ""
	if sealedPassword != "" {
		if u.importSealer == nil {
			return ArchiveImportStart{}, model.ErrPlatform.WithMessage("The archive password cannot be opened: secret storage is not configured").Err()
		}
		plain, err := u.importSealer.DecryptWithContext(sealedPassword, passwordContext(job.ID))
		if err != nil {
			return ArchiveImportStart{}, model.ErrPlatform.WithError(err).WithMessage("The archive password cannot be opened").Err()
		}
		password = string(plain)
	}
	q, ok := u.repo.(interface {
		GetUserByID(context.Context, uuid.UUID) (postgres.User, error)
	})
	if !ok {
		return ArchiveImportStart{}, model.ErrPlatform.WithMessage("Import actor source is not wired").Err()
	}
	actor, err := u.currentImportActor(ctx, q, job.CreatedBy)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	var options ImportOptions
	if len(job.Options) > 0 {
		if err = json.Unmarshal(job.Options, &options); err != nil {
			return ArchiveImportStart{}, err
		}
	}
	in := CommitArchiveInput{PreviewArchiveInput: PreviewArchiveInput{Password: password, OwnerEventID: uuidPtr(job.OwnerEventID), Targets: options.Targets, FixedExerciseID: options.FixedExerciseID}, ConfirmedUpdates: options.ConfirmedUpdates}
	switch job.Source {
	case exerciseModel.ImportSourceUpload:
		in.UploadID = job.SourceID
	case exerciseModel.ImportSourceFile:
		in.FileID = job.SourceID
	default:
		return ArchiveImportStart{}, model.ErrPlatform.WithMessage("Unknown archive source").Err()
	}
	reader, size, err := u.previewArchiveSource(ctx, actor, in.PreviewArchiveInput)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	// One set of limits for the whole import.
	lim := u.ArchiveLimits(ctx)
	if err = checkArchiveSize(size, lim.WithImagesBytes()); err != nil {
		return ArchiveImportStart{}, err
	}
	entries, protected, err := openZipAt(reader, size, password, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	read, err := readArchiveEntries(entries, protected, lim)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	return u.commitArchiveRead(ctx, actor, in, read, &job, progress)
}

// importStateTimeout bounds one write of an import's outcome.
const importStateTimeout = 10 * time.Second

// writeImportJob writes the job on a context that outlives the job's (cancellation, timeout).
func (u *ExerciseUseCase) writeImportJob(ctx context.Context, job exerciseModel.ImportJob) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), importStateTimeout)
	defer cancel()
	if n, err := u.exercises.SaveImportJob(c, job); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record the import").Err()
	} else if n != 1 {
		return errStaleImportAttempt
	}
	return nil
}

// dropArchiveUpload removes an imported archive's upload. Best effort: the upload expiry removes what is left.
func (u *ExerciseUseCase) dropArchiveUpload(ctx context.Context, id uuid.UUID) {
	if err := u.archiveSources.DropUpload(ctx, id); err != nil {
		log.Warn().Err(err).Str("upload", id.String()).Msg("archive import: dropping the upload failed, its expiry removes it")
	}
}

// importJobFailure is what the user of a failed import is told: a typed error's code, message and public context
// (an internal one only as "Internal server error"); an untyped failure of the archive itself (a broken zip, a wrong
// password; IsArchiveFormatError) as invalid data with its text, like the synchronous import answers it; any other
// untyped failure as the generic internal error (its text stays in the server log).
func importJobFailure(failure error) exerciseModel.ImportJobError {
	var typed appErr.Error
	if errors.As(failure, &typed) {
		e := typed.UnwrapNotInternalError()
		if e.StatusCode().IsInternal() {
			return exerciseModel.ImportJobError{Code: e.StatusCode().FullCode(), Message: "Internal server error"}
		}
		return exerciseModel.ImportJobError{Code: e.StatusCode().FullCode(), Message: e.StatusCode().Message(), Context: e.PublicContext()}
	}
	if IsArchiveFormatError(failure) {
		code := appErr.ErrInvalidData.Err().StatusCode()
		return exerciseModel.ImportJobError{Code: code.FullCode(), Message: failure.Error()}
	}
	code := model.ErrPlatform.Err().StatusCode()
	return exerciseModel.ImportJobError{Code: code.FullCode(), Message: "Internal server error"}
}
