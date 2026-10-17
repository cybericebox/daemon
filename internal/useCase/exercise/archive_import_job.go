package exercise

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
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
	}

	// QueueImportInput names the archive source of a background import.
	QueueImportInput struct {
		Source    exerciseModel.ImportSource
		SourceID  uuid.UUID
		Password  string
		CreatedBy uuid.UUID
	}

	// ArchiveImportStart is either the tasks created at once (no image files) or the background import's job.
	ArchiveImportStart struct {
		Exercises []ExerciseView
		JobID     uuid.UUID
	}

	// ImportJobView is an import job as its user sees it.
	ImportJobView struct {
		ID          uuid.UUID
		State       exerciseModel.ImportJobState
		ExerciseIDs []uuid.UUID
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
	entries, protected, err := openZipAt(reader, upload.SizeBytes, in.Password)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	named, err := namesImageFiles(entries, protected)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	if named {
		jobID, queueErr := u.QueueArchiveImport(ctx, QueueImportInput{
			Source: exerciseModel.ImportSourceUpload, SourceID: in.UploadID, Password: in.Password, CreatedBy: in.CreatedBy,
		})
		if queueErr != nil {
			return ArchiveImportStart{}, queueErr
		}
		return ArchiveImportStart{JobID: jobID}, nil
	}
	// Without image files the archive limits are the synchronous import's.
	if err = checkArchiveSize(int(upload.SizeBytes)); err != nil {
		return ArchiveImportStart{}, err
	}
	contents, err := ReadArchiveBytes(io.NewSectionReader(reader, 0, upload.SizeBytes))
	if err != nil {
		return ArchiveImportStart{}, err
	}
	created, err := u.ImportArchiveBundle(ctx, contents, in.Password, in.CreatedBy)
	if err != nil {
		return ArchiveImportStart{}, err
	}
	u.dropArchiveUpload(ctx, in.UploadID)
	return ArchiveImportStart{Exercises: created}, nil
}

// ImportArchiveBundle imports every task of an archive held in memory (one task folder or several), each as its
// own import: the synchronous path of archives without image files.
func (u *ExerciseUseCase) ImportArchiveBundle(ctx context.Context, contents []byte, password string, createdBy uuid.UUID) ([]ExerciseView, error) {
	imports, err := ExpandExerciseArchiveBundle(contents, password)
	if err != nil {
		return nil, err
	}
	created := make([]ExerciseView, 0, len(imports))
	for _, item := range imports {
		view, importErr := u.ImportExerciseArchive(ctx, ImportExerciseInput{Archive: item.Archive, Password: item.Password, CreatedBy: createdBy})
		if importErr != nil {
			return nil, importErr
		}
		created = append(created, view)
	}
	return created, nil
}

// QueueArchiveImport records the background import of an archive source and queues it; it returns the job id.
// One import per source: a running or done import of it is returned as it is, a failed one runs again. The
// registry not configured: the upload refusal, nothing recorded.
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
	job, err := u.exercises.StartImportJob(ctx, exerciseModel.NewImportJob(newID, in.CreatedBy, in.SourceID, in.Source, now))
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to record the import").Err()
	}
	if !job.OwnedBy(in.CreatedBy) {
		return uuid.Nil, exerciseModel.ErrImportJobNotFound.Err()
	}
	if job.ID != newID { // the source already has an import
		if !job.Restart(now) {
			return job.ID, nil // running or done: the same import
		}
		if _, err = u.exercises.SaveImportJob(ctx, job); err != nil {
			return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to record the import").Err()
		}
	}
	args := jobsModel.ExerciseImportArgs{JobID: job.ID}
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
	view := ImportJobView{ID: job.ID, State: state, Error: failure}
	if state == exerciseModel.ImportJobDone {
		view.ExerciseIDs = job.ExerciseIDs
	}
	return view, nil
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
	if job.State != exerciseModel.ImportJobRunning {
		return nil
	}
	created, runErr := u.runArchiveImport(ctx, job, args.SealedPassword)
	if runErr != nil {
		log.Warn().Err(runErr).Str("job", job.ID.String()).Msg("archive import failed")
		failure := importJobFailure(runErr)
		if ctx.Err() != nil {
			// Cut off (timeout, shutdown): whatever the last call failed with, the import was stopped.
			failure = importJobFailure(exerciseModel.ErrArchiveImportStopped.WithError(runErr).Err())
		}
		job.Fail(failure, u.timeNow())
		// Nothing was created: if the failure cannot be recorded, River may run the import again.
		return u.writeImportJobRetrying(ctx, job)
	}
	ids := make([]uuid.UUID, len(created))
	for i, view := range created {
		ids[i] = view.ID
	}
	job.Finish(ids, u.timeNow())
	if job.Source == exerciseModel.ImportSourceUpload {
		u.dropArchiveUpload(context.WithoutCancel(ctx), job.SourceID)
	}
	// The tasks exist: the job must never run again, so the outcome is retried here and never returned to River.
	// Not written at all, the job reads failed (interrupted) once stale, with the tasks already in the catalog.
	if err = u.writeImportJobRetrying(ctx, job); err != nil {
		log.Error().Err(err).Str("job", job.ID.String()).Msg("archive import: the finished import could not be recorded")
	}
	return nil
}

// importStateWrites is how often an import's outcome is written before giving up.
const importStateWrites = 5

// writeImportJobRetrying writes the job up to importStateWrites times, importStateRetryDelay apart.
func (u *ExerciseUseCase) writeImportJobRetrying(ctx context.Context, job exerciseModel.ImportJob) error {
	var err error
	for attempt := 0; attempt < importStateWrites; attempt++ {
		if attempt > 0 {
			time.Sleep(importStateRetryDelay)
		}
		if err = u.writeImportJob(ctx, job); err == nil {
			return nil
		}
	}
	return err
}

// runArchiveImport opens the source and the password and imports the archive.
func (u *ExerciseUseCase) runArchiveImport(ctx context.Context, job exerciseModel.ImportJob, sealedPassword string) ([]ExerciseView, error) {
	if u.archiveSources == nil {
		return nil, model.ErrPlatform.WithMessage("Archive import is not wired").Err()
	}
	password := ""
	if sealedPassword != "" {
		if u.importSealer == nil {
			return nil, model.ErrPlatform.WithMessage("The archive password cannot be opened: secret storage is not configured").Err()
		}
		plain, err := u.importSealer.DecryptWithContext(sealedPassword, passwordContext(job.ID))
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("The archive password cannot be opened").Err()
		}
		password = string(plain)
	}
	var (
		reader io.ReaderAt
		size   int64
		err    error
	)
	switch job.Source {
	case exerciseModel.ImportSourceUpload:
		var upload mediaModel.Upload
		reader, upload, err = u.archiveSources.OpenUploadAt(ctx, job.SourceID)
		size = upload.SizeBytes
	case exerciseModel.ImportSourceFile:
		var file mediaModel.File
		reader, file, err = u.archiveSources.OpenFileAt(ctx, job.SourceID)
		size = file.SizeBytes
	default:
		err = model.ErrPlatform.WithMessage("Unknown archive source").Err()
	}
	if err != nil {
		return nil, err
	}
	if size > exerciseModel.MaxArchiveWithImagesBytes {
		return nil, archiveTooLarge("the archive is over " + mib(exerciseModel.MaxArchiveWithImagesBytes))
	}
	entries, protected, err := openZipAt(reader, size, password)
	if err != nil {
		return nil, err
	}
	read, err := readArchiveEntries(entries, protected)
	if err != nil {
		return nil, err
	}
	return u.importArchiveRead(ctx, read, job.CreatedBy)
}

// importStateTimeout bounds one write of an import's outcome.
const importStateTimeout = 10 * time.Second

// writeImportJob writes the job on a context that outlives the job's (cancellation, timeout).
func (u *ExerciseUseCase) writeImportJob(ctx context.Context, job exerciseModel.ImportJob) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), importStateTimeout)
	defer cancel()
	if _, err := u.exercises.SaveImportJob(c, job); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record the import").Err()
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
