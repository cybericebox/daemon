package exercise

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	yzip "github.com/yeka/zip"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Background export of tasks «з образами». The platform images of the devices make an archive of up to 512 MiB +
// 10 GiB, so the export never runs inside a request: the request records an export job and queues it; the job
// (RunArchiveExport) lays the tasks out, then streams the zip (small files from memory, every image straight from the
// registry) into storage without holding it whole, and records done (the stored file, read in chunks like any export)
// or failed (the error the user sees); ArchiveExportJob reads it, for the user who started it only.
//
// The archive password (an export with secrets) is sealed with the platform cipher, bound to the job id, into the
// River job's arguments; it is opened only by the job and never stored in plain text.

type (
	// IExportFiles stores the finished zip as a file of its creator (the media use case).
	IExportFiles interface {
		UploadLargeFile(ctx context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID, maxBytes int64) (mediaModel.File, error)
		GetFile(ctx context.Context, id uuid.UUID) (mediaModel.File, error)
	}

	// ExportJobQueue queues the background export (unique per job while queued or running).
	ExportJobQueue func(ctx context.Context, args jobsModel.ExerciseExportArgs) error

	// ArchiveExportDependencies wire the background export; any of them nil: exports with images are refused as a
	// platform error (the export without images still works).
	ArchiveExportDependencies struct {
		Images IImageExporter
		Files  IExportFiles
		Queue  ExportJobQueue
		// Sealer seals the archive password into the job (PLATFORM_SECRETS_KEY).
		Sealer secret.Sealer
	}

	// StartExportInput names the tasks of a background export and its options.
	StartExportInput struct {
		IDs            []uuid.UUID
		IncludeSecrets bool
		Password       string
		// FileName is the name the stored zip gets.
		FileName  string
		CreatedBy uuid.UUID
	}

	// ExportJobView is an export job as its user sees it.
	ExportJobView struct {
		ID    uuid.UUID
		State exerciseModel.ImportJobState
		// File is the stored zip (done only).
		File  *mediaModel.File
		Error *exerciseModel.ImportJobError
	}
)

// SetArchiveExport wires the background export of tasks with images.
func (u *ExerciseUseCase) SetArchiveExport(deps ArchiveExportDependencies) {
	u.imageExporter, u.exportFiles, u.exportQueue, u.exportSealer = deps.Images, deps.Files, deps.Queue, deps.Sealer
}

// exportPasswordContext binds a sealed archive password to its job.
func exportPasswordContext(jobID uuid.UUID) []byte {
	return []byte("exercise-export:password:" + jobID.String())
}

// StartArchiveExport records the background export of the tasks «з образами» and queues it; it returns the job id.
func (u *ExerciseUseCase) StartArchiveExport(ctx context.Context, in StartExportInput) (uuid.UUID, error) {
	if u.imageExporter == nil || u.exportFiles == nil || u.exportQueue == nil {
		return uuid.Nil, model.ErrPlatform.WithMessage("Image export is not wired").Err()
	}
	if in.IncludeSecrets && in.Password == "" {
		return uuid.Nil, model.ErrPlatform.WithMessage("An export with secrets needs a password").Err()
	}
	if in.IncludeSecrets && u.exportSealer == nil {
		return uuid.Nil, model.ErrPlatform.WithMessage("The archive password cannot be sealed: secret storage is not configured").Err()
	}
	job := exerciseModel.NewExportJob(freshID(), in.CreatedBy, in.IDs, in.FileName, u.timeNow())
	if err := u.exercises.CreateExportJob(ctx, job); err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to record the export").Err()
	}
	args := jobsModel.ExerciseExportArgs{JobID: job.ID, IncludeSecrets: in.IncludeSecrets}
	if in.IncludeSecrets {
		sealed, err := u.exportSealer.EncryptWithContext([]byte(in.Password), exportPasswordContext(job.ID))
		if err != nil {
			return uuid.Nil, u.failQueuedExport(ctx, job, model.ErrPlatform.WithError(err).WithMessage("Failed to seal the archive password").Err())
		}
		args.SealedPassword = sealed
	}
	if err := u.exportQueue(ctx, args); err != nil {
		return uuid.Nil, u.failQueuedExport(ctx, job, model.ErrPlatform.WithError(err).WithMessage("Failed to queue the export").Err())
	}
	return job.ID, nil
}

// failQueuedExport records a job that could not be queued as failed and returns the cause.
func (u *ExerciseUseCase) failQueuedExport(ctx context.Context, job exerciseModel.ExportJob, cause error) error {
	job.Fail(importJobFailure(cause), u.timeNow())
	if err := u.writeExportJob(ctx, job); err != nil {
		log.Warn().Err(err).Str("job", job.ID.String()).Msg("archive export: recording the queue failure failed")
	}
	return cause
}

// ArchiveExportJob reads an export job of user; another user's job is not found, like an absent one.
func (u *ExerciseUseCase) ArchiveExportJob(ctx context.Context, id, user uuid.UUID) (ExportJobView, error) {
	job, err := u.exercises.GetExportJob(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ExportJobView{}, exerciseModel.ErrExportJobNotFound.WithError(err).Err()
		}
		return ExportJobView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the export").Err()
	}
	if !job.OwnedBy(user) {
		return ExportJobView{}, exerciseModel.ErrExportJobNotFound.Err()
	}
	state, failure := job.Current(u.timeNow())
	view := ExportJobView{ID: job.ID, State: state, Error: failure}
	if state == exerciseModel.ImportJobDone && job.FileID != uuid.Nil {
		file, fileErr := u.exportFiles.GetFile(ctx, job.FileID)
		if fileErr != nil {
			return ExportJobView{}, fileErr
		}
		view.File = &file
	}
	return view, nil
}

// RunArchiveExport is the background export (River job exercise_export): it lays the tasks out, streams the zip into
// storage and records done or failed. Only a failure to record the outcome is returned (River retries then). The
// outcome is written on a context of its own, so an export cut off by its timeout still ends recorded as failed.
func (u *ExerciseUseCase) RunArchiveExport(ctx context.Context, args jobsModel.ExerciseExportArgs) error {
	job, err := u.exercises.GetExportJob(ctx, args.JobID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil // the user is gone with the job: nothing to export
		}
		return err
	}
	if job.State != exerciseModel.ImportJobRunning {
		return nil
	}
	file, runErr := u.runArchiveExport(ctx, job, args)
	if runErr != nil {
		log.Warn().Err(runErr).Str("job", job.ID.String()).Msg("archive export failed")
		failure := importJobFailure(runErr)
		if ctx.Err() != nil {
			// Cut off (timeout, shutdown): whatever the last call failed with, the export was stopped.
			failure = importJobFailure(exerciseModel.ErrArchiveExportStopped.WithError(runErr).Err())
		}
		job.Fail(failure, u.timeNow())
		return u.writeExportJobRetrying(ctx, job)
	}
	job.Finish(file.ID, u.timeNow())
	// The file exists: the job must never run again, so the outcome is retried here and never returned to River.
	// Not written at all, the job reads failed (interrupted) once stale, with the file left to the media cleanup.
	if err = u.writeExportJobRetrying(ctx, job); err != nil {
		log.Error().Err(err).Str("job", job.ID.String()).Msg("archive export: the finished export could not be recorded")
	}
	return nil
}

// runArchiveExport opens the password, lays every task out and stores the zip.
func (u *ExerciseUseCase) runArchiveExport(ctx context.Context, job exerciseModel.ExportJob, args jobsModel.ExerciseExportArgs) (mediaModel.File, error) {
	if u.exportFiles == nil {
		return mediaModel.File{}, model.ErrPlatform.WithMessage("Image export is not wired").Err()
	}
	opts := ExportOptions{IncludeSecrets: args.IncludeSecrets, IncludeImages: true}
	if args.IncludeSecrets {
		if args.SealedPassword == "" || u.exportSealer == nil {
			return mediaModel.File{}, model.ErrPlatform.WithMessage("The archive password cannot be opened: secret storage is not configured").Err()
		}
		plain, err := u.exportSealer.DecryptWithContext(args.SealedPassword, exportPasswordContext(job.ID))
		if err != nil {
			return mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("The archive password cannot be opened").Err()
		}
		opts.Password = string(plain)
	}
	plans := make([]exportPlan, 0, len(job.ExerciseIDs))
	for _, id := range job.ExerciseIDs {
		plan, err := u.planExport(ctx, id, opts)
		if err != nil {
			return mediaModel.File{}, err
		}
		plans = append(plans, plan)
	}
	entries, images, err := layoutExport(plans)
	if err != nil {
		return mediaModel.File{}, err
	}
	return u.storeExport(ctx, job, entries, images, opts)
}

// layoutExport puts the tasks into one archive: a single task at the zip root, several each in a folder named after
// it. It refuses what the import would refuse: the small files keep the archive limits, the images are counted apart.
func layoutExport(plans []exportPlan) (map[string][]byte, map[string]ExportedImage, error) {
	prefixes := make([]string, len(plans))
	if len(plans) > 1 {
		names := make([]string, len(plans))
		for i, plan := range plans {
			names[i] = plan.Name
		}
		for i, folder := range bundleFolders(names) {
			prefixes[i] = folder + "/"
		}
	}
	entries := map[string][]byte{}
	images := map[string]ExportedImage{}
	for i, plan := range plans {
		for name, body := range plan.Contents {
			entries[prefixes[i]+name] = body
		}
		for _, image := range plan.Images {
			images[prefixes[i]+image.Path] = image.Image
		}
	}
	if err := checkExportEntries(entries); err != nil {
		return nil, nil, err
	}
	if len(entries)+len(images)+1 > limits.Entries {
		return nil, nil, archiveTooLarge(fmt.Sprintf("the archive has more than %d entries", limits.Entries))
	}
	return entries, images, nil
}

// errUploadAborted closes the pipe from the reading side: the store failed, so the writer stops.
var errUploadAborted = errors.New("the export could not be stored")

// storeExport writes the zip into a pipe that the media use case stores as a file, so the zip is never held whole.
// A failure of the zip writer (an image the registry no longer has) is returned as it is, not as the store's failure
// to read the pipe.
func (u *ExerciseUseCase) storeExport(ctx context.Context, job exerciseModel.ExportJob, entries map[string][]byte, images map[string]ExportedImage, opts ExportOptions) (mediaModel.File, error) {
	reader, writer := io.Pipe()
	var writeErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := writeExportZip(ctx, writer, entries, images, opts)
		if err != nil && !errors.Is(err, errUploadAborted) {
			writeErr = err
		}
		_ = writer.CloseWithError(err)
	}()
	file, err := u.exportFiles.UploadLargeFile(ctx, job.FileName, "application/zip", reader, job.CreatedBy, exerciseModel.MaxArchiveWithImagesBytes)
	if err != nil {
		_ = reader.CloseWithError(errUploadAborted)
	}
	<-done
	if writeErr != nil {
		return mediaModel.File{}, writeErr
	}
	return file, err
}

// zipEntries creates the entries of a zip, plain or password-protected (AES); the entry is stored as it is (Store)
// when it does not compress.
type zipEntries interface {
	create(name string, store bool) (io.Writer, error)
	close() error
}

type plainZip struct{ w *zip.Writer }

func (z plainZip) create(name string, store bool) (io.Writer, error) {
	method := zip.Deflate
	if store {
		method = zip.Store
	}
	return z.w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
}

func (z plainZip) close() error { return z.w.Close() }

type protectedZip struct {
	w        *yzip.Writer
	password string
}

func (z protectedZip) create(name string, store bool) (io.Writer, error) {
	method := yzip.Deflate
	if store {
		method = yzip.Store
	}
	header := &yzip.FileHeader{Name: name, Method: method}
	header.SetPassword(z.password)
	header.SetEncryptionMethod(yzip.AES256Encryption)
	return z.w.CreateHeader(header)
}

func (z protectedZip) close() error { return z.w.Close() }

// writeExportZip writes the archive: every entry by name, the images streamed (stored, their bytes hashed on the way),
// then manifest.json with the checksum of every entry. The result does not depend on map order.
func writeExportZip(ctx context.Context, w io.Writer, entries map[string][]byte, images map[string]ExportedImage, opts ExportOptions) error {
	var zw zipEntries = plainZip{zip.NewWriter(w)}
	if opts.IncludeSecrets {
		zw = protectedZip{w: yzip.NewWriter(w), password: opts.Password}
	}
	names := make([]string, 0, len(entries)+len(images))
	for name := range entries {
		names = append(names, name)
	}
	for name := range images {
		names = append(names, name)
	}
	sort.Strings(names)
	checksums := make(map[string]string, len(names))
	for _, name := range names {
		if !safeArchivePath(name) {
			return fmt.Errorf("unsafe archive path")
		}
		if body, ok := entries[name]; ok {
			out, err := zw.create(name, expandsTooFar(body))
			if err != nil {
				return err
			}
			if _, err = out.Write(body); err != nil {
				return err
			}
			checksums[name] = hashBytes(body)
			continue
		}
		sum, err := writeExportImage(ctx, zw, name, images[name])
		if err != nil {
			return err
		}
		checksums[name] = sum
	}
	manifest, err := json.Marshal(archiveManifest{Version: 1, Checksums: checksums})
	if err != nil {
		return err
	}
	out, err := zw.create("manifest.json", false)
	if err != nil {
		return err
	}
	if _, err = out.Write(manifest); err != nil {
		return err
	}
	return zw.close()
}

// writeExportImage streams one image archive into an entry and returns its SHA-256.
func writeExportImage(ctx context.Context, zw zipEntries, name string, image ExportedImage) (string, error) {
	out, err := zw.create(name, true)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	counter := new(countingWriter)
	if err = image.WriteTo(ctx, io.MultiWriter(out, hash, counter)); err != nil {
		return "", err
	}
	if int64(*counter) != image.Size() {
		return "", model.ErrPlatform.WithMessage("The image archive is not the size it was announced with").Err()
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// exportStateWrites is how often an export's outcome is written before giving up.
const exportStateWrites = 5

// writeExportJobRetrying writes the job up to exportStateWrites times, importStateRetryDelay apart; the wait ends
// early when ctx does (the last write's error is returned then).
func (u *ExerciseUseCase) writeExportJobRetrying(ctx context.Context, job exerciseModel.ExportJob) error {
	var err error
	for attempt := 0; attempt < exportStateWrites; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(importStateRetryDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return err
			}
		}
		if err = u.writeExportJob(ctx, job); err == nil {
			return nil
		}
	}
	return err
}

// writeExportJob writes the job on a context that outlives the job's (cancellation, timeout).
func (u *ExerciseUseCase) writeExportJob(ctx context.Context, job exerciseModel.ExportJob) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), importStateTimeout)
	defer cancel()
	if _, err := u.exercises.SaveExportJob(c, job); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record the export").Err()
	}
	return nil
}
