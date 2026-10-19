package exercise

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	mediaUseCase "github.com/cybericebox/daemon/internal/useCase/media"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

type exportExercisesRequest struct {
	IDs            []uuid.UUID `json:"IDs" binding:"required,min=1,max=100"`
	IncludeSecrets bool        `json:"IncludeSecrets"`
	Password       string      `json:"Password"`
	// IncludeImages writes the platform images of the devices into the archive («з образами», up to 10 GiB): the export
	// then runs in the background, 202 {JobID}.
	IncludeImages bool `json:"IncludeImages"`
}

type exportedFileResponse struct {
	// FileID is the stored zip; its chunks are read with GET /exercises/files/{fileID}/chunks/{index}. The file is
	// kept for the media grace window (24 h by default), then the cleanup removes it.
	FileID uuid.UUID `json:"FileID"`
	Name   string    `json:"Name"`
	Size   int64     `json:"Size"`
	// ChunkSize is the size of every chunk but the last.
	ChunkSize int64 `json:"ChunkSize"`
	Chunks    int   `json:"Chunks"`
}

// export godoc
// @Summary      Export selected exercises as one ZIP archive stored as a file (several: one folder per exercise)
// @Description  exercises.export exports any task; an event owner/manager only the tasks of their events (403
// @Description  otherwise, nothing exported). 200 with the stored file. With IncludeImages (the platform images of the devices go into the archive,
// @Description  `images/…/<device>.tar`) the export runs in the background: 202 {JobID}; poll GET
// @Description  /exercises/export/jobs/{jobID}, then read the file in chunks.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  exportExercisesRequest  true  "selection and options"
// @Success  200  {object}  response.Response{data=exportedFileResponse}
// @Success  202  {object}  response.Response{data=exportJobStarted}
// @Failure  400  {object}  response.Response
// @Router   /exercises/export [post]
func (h *Handler) export(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req exportExercisesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if req.IncludeSecrets && strings.TrimSpace(req.Password) == "" {
		response.AbortWithBadRequest(ctx, fmt.Errorf("a password is required when exporting secrets"))
		return
	}
	// Every task of the selection, before anything is read or recorded.
	if err := h.useCase.AuthorizeExport(ctx, exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}, req.IDs); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if req.IncludeImages {
		h.startImageExport(ctx, claims.UserID, req)
		return
	}
	// One set of archive limits for every task of the export and the bundle.
	lim := h.useCase.ArchiveLimits(ctx)
	items := make([]exerciseUseCase.NamedArchive, 0, len(req.IDs))
	for _, id := range req.IDs {
		exercise, err := h.useCase.GetExercise(ctx, id)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		archive, err := h.useCase.ExportExerciseArchive(ctx, id, exerciseUseCase.ExportOptions{IncludeSecrets: req.IncludeSecrets, Password: req.Password}, lim)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		items = append(items, exerciseUseCase.NamedArchive{Name: exercise.Name, Archive: archive})
	}
	name, contents := archiveFilename(items[0].Name), items[0].Archive
	if len(items) > 1 {
		password := ""
		if req.IncludeSecrets {
			password = req.Password
		}
		// One zip, one folder per task, one root manifest.json.
		bundle, err := exerciseUseCase.BundleExerciseArchives(items, password, lim)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		name, contents = "exercises.cybericebox.zip", bundle
	}
	// The zip becomes a stored file of its creator; nothing references it, so the media cleanup removes it after the
	// grace window: that is the time to download it.
	stored, err := h.useCase.UploadFile(ctx, name, "application/zip", bytes.NewReader(contents), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	chunkSize := h.useCase.UploadChunkBytes()
	response.AbortWithData(ctx, exportedFileResponse{
		FileID: stored.ID, Name: stored.Name, Size: stored.SizeBytes, ChunkSize: chunkSize, Chunks: mediaUseCase.ChunkCount(stored.SizeBytes, chunkSize),
	})
}

// exportJobStarted is the answer of an export that runs in the background (IncludeImages): poll
// GET /exercises/export/jobs/{JobID}.
type exportJobStarted struct {
	JobID uuid.UUID `json:"JobID"`
}

// startImageExport records the background export «з образами» of the tasks and answers 202 {JobID}. Every task is
// read first, so an unknown one is refused before any job is recorded.
func (h *Handler) startImageExport(ctx *gin.Context, user uuid.UUID, req exportExercisesRequest) {
	var firstName string
	for i, id := range req.IDs {
		exercise, err := h.useCase.GetExercise(ctx, id)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		if i == 0 {
			firstName = exercise.Name
		}
	}
	name := archiveFilename(firstName)
	if len(req.IDs) > 1 {
		name = "exercises.cybericebox.zip"
	}
	jobID, err := h.useCase.StartArchiveExport(ctx, exerciseUseCase.StartExportInput{
		IDs: req.IDs, IncludeSecrets: req.IncludeSecrets, Password: req.Password, FileName: name, CreatedBy: user,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithDataStatus(ctx, http.StatusAccepted, exportJobStarted{JobID: jobID})
}

type exportJobResponse struct {
	// State is running, done or failed.
	State string `json:"State"`
	// File is the stored zip, read with GET /exercises/files/{FileID}/chunks/{index} (done only).
	File *exportedFileResponse `json:"File,omitempty"`
	// Error is why it failed (failed only).
	Error *importJobError `json:"Error,omitempty"`
}

// exportJob godoc
// @Summary      State of a background export «з образами»
// @Description  running → done (File: the stored zip, read in chunks like any export) or failed (Error: Code, Message,
// @Description  Context). Only the user who started it sees it (404 otherwise). The file is kept for the media grace
// @Description  window (24 h by default).
// @Tags     exercises
// @Produce  json
// @Param    jobID  path  string  true  "export job ID"
// @Success  200  {object}  response.Response{data=exportJobResponse}
// @Failure  404  {object}  response.Response
// @Router   /exercises/export/jobs/{jobID} [get]
func (h *Handler) exportJob(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("jobID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	job, err := h.useCase.ArchiveExportJob(ctx, id, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := exportJobResponse{State: string(job.State)}
	if job.Error != nil {
		out.Error = &importJobError{Code: job.Error.Code, Message: job.Error.Message, Context: job.Error.Context}
	}
	if job.File != nil {
		chunkSize := h.useCase.UploadChunkBytes()
		out.File = &exportedFileResponse{
			FileID: job.File.ID, Name: job.File.Name, Size: job.File.SizeBytes, ChunkSize: chunkSize, Chunks: mediaUseCase.ChunkCount(job.File.SizeBytes, chunkSize),
		}
	}
	response.AbortWithData(ctx, out)
}

// importArchive godoc
// @Summary      Import an exercise ZIP archive
// @Description  200 with the created tasks; an archive naming device image files is stored as a file and imported
// @Description  in the background: 202 {JobID} (see GET /exercises/import/jobs/{jobID}).
// @Tags     exercises
// @Accept   multipart/form-data
// @Produce  json
// @Param    archive   formData  file    true   "exercise ZIP"
// @Param    password  formData  string  false  "archive password when it contains secrets"
// @Param    OwnerEventID  formData  string  false  "the event the tasks are created in (its owner/manager, or exercises.write); empty: the platform catalog (exercises.write)"
// @Success  200  {object}  response.Response{data=[]exerciseResponse}
// @Success  202  {object}  response.Response{data=importJobStarted}
// @Failure  400  {object}  response.Response
// @Router   /exercises/import [post]
func (h *Handler) importArchive(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	actor := exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}
	// Nobody who may not import anywhere makes the server read an archive; the target is checked once the form is read.
	if err := h.useCase.RequireAuthor(ctx, actor); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	// One set of archive limits for the whole import.
	lim := h.useCase.ArchiveLimits(ctx)
	middleware.LimitBody(ctx, lim.ArchiveBytes+multipartOverheadSlack)
	file, err := ctx.FormFile("archive")
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	input, err := file.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = input.Close() }()
	contents, err := exerciseUseCase.ReadArchiveBytes(input, lim.ArchiveBytes)
	if err != nil {
		abortArchiveError(ctx, err)
		return
	}
	owner, ok := ownerEventFromForm(ctx)
	if !ok {
		return
	}
	if err = h.useCase.AuthorizeImport(ctx, actor, owner); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	password := ctx.PostForm("password")
	if exerciseUseCase.ArchiveNamesImages(contents, password, lim) {
		// Images need the platform registry: refuse before anything is stored.
		if err = h.useCase.RequireImageUploads(ctx); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		// The background import reads a stored archive: the zip becomes a file of the caller (nothing references
		// it, so the media cleanup removes it after the grace window).
		stored, storeErr := h.useCase.UploadFile(ctx, archiveFilename(strings.TrimSuffix(file.Filename, ".zip")), "application/zip", bytes.NewReader(contents), claims.UserID)
		if storeErr != nil {
			response.AbortWithError(ctx, storeErr)
			return
		}
		h.queueImport(ctx, exerciseUseCase.QueueImportInput{Source: exerciseModel.ImportSourceFile, SourceID: stored.ID, Password: password, CreatedBy: claims.UserID, OwnerEventID: owner})
		return
	}
	h.importContents(ctx, claims.UserID, owner, contents, password, lim)
}

// ownerEventFromForm reads the optional OwnerEventID form field: nil is the platform catalog.
func ownerEventFromForm(ctx *gin.Context) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(ctx.PostForm("OwnerEventID"))
	if raw == "" {
		return nil, true
	}
	id, err := uuid.FromString(raw)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return nil, false
	}
	return &id, true
}

// importExample godoc
// @Summary  Download the example import archive (a small built-in ZIP of sample tasks)
// @Tags     exercises
// @Produce  application/zip
// @Success  200  {file}  binary
// @Failure  500  {object}  response.Response
// @Router   /exercises/import/example [get]
func (h *Handler) importExample(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RequireAuthor(ctx, actor); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	contents, err := exerciseUseCase.ExampleArchive()
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	serveArchive(ctx, exerciseUseCase.ExampleArchiveFilename, contents)
}

// serveArchive answers with a small zip held in memory as one attachment.
func serveArchive(ctx *gin.Context, name string, contents []byte) {
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Data(http.StatusOK, "application/zip", contents)
}

type importUploadedRequest struct {
	// FileID is the file a chunked upload produced (POST /exercises/uploads/{uploadID}/complete).
	FileID   uuid.UUID `json:"FileID" binding:"required"`
	Password string    `json:"Password"`
	// OwnerEventID is the event the tasks are created in (its owner/manager, or exercises.write); null: the
	// platform catalog (exercises.write).
	OwnerEventID *uuid.UUID `json:"OwnerEventID"`
}

// importUploadedArchive godoc
// @Summary      Import an exercise ZIP archive that went up in chunks (completed into a file, at most the archive limit)
// @Description  200 with the created tasks; an archive naming device image files is imported in the background:
// @Description  202 {JobID} (see GET /exercises/import/jobs/{jobID}). Larger archives use POST /exercises/import/upload.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  importUploadedRequest  true  "the uploaded file and the archive password"
// @Success  200  {object}  response.Response{data=[]exerciseResponse}
// @Success  202  {object}  response.Response{data=importJobStarted}
// @Failure  400  {object}  response.Response
// @Router   /exercises/import/uploaded [post]
func (h *Handler) importUploadedArchive(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req importUploadedRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.AuthorizeImport(ctx, exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}, req.OwnerEventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	// Only a file the caller uploaded: another person's file is not found, like an absent one.
	meta, err := h.useCase.GetFile(ctx, req.FileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !meta.CreatedBy.Valid || meta.CreatedBy.UUID != claims.UserID {
		response.AbortWithError(ctx, mediaModel.ErrFileNotFound.Err())
		return
	}
	reader, _, err := h.useCase.StreamFile(ctx, req.FileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	// One set of archive limits for the whole import.
	lim := h.useCase.ArchiveLimits(ctx)
	contents, err := exerciseUseCase.ReadArchiveBytes(reader, lim.ArchiveBytes)
	if err != nil {
		abortArchiveError(ctx, err)
		return
	}
	if exerciseUseCase.ArchiveNamesImages(contents, req.Password, lim) {
		h.queueImport(ctx, exerciseUseCase.QueueImportInput{Source: exerciseModel.ImportSourceFile, SourceID: req.FileID, Password: req.Password, CreatedBy: claims.UserID, OwnerEventID: req.OwnerEventID})
		return
	}
	h.importContents(ctx, claims.UserID, req.OwnerEventID, contents, req.Password, lim)
}

// importJobStarted is the answer of an import that runs in the background (an archive naming device image files):
// poll GET /exercises/import/jobs/{JobID}.
type importJobStarted struct {
	JobID uuid.UUID `json:"JobID"`
}

// queueImport starts the background import of a stored archive and answers 202 {JobID}.
func (h *Handler) queueImport(ctx *gin.Context, in exerciseUseCase.QueueImportInput) {
	jobID, err := h.useCase.QueueArchiveImport(ctx, in)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithDataStatus(ctx, http.StatusAccepted, importJobStarted{JobID: jobID})
}

type importUploadRequest struct {
	// UploadID is a chunked upload with Purpose "exercise-archive", every chunk sent (no complete call).
	UploadID uuid.UUID `json:"UploadID" binding:"required"`
	Password string    `json:"Password"`
	// OwnerEventID is the event the tasks are created in (its owner/manager, or exercises.write); null: the
	// platform catalog (exercises.write).
	OwnerEventID *uuid.UUID `json:"OwnerEventID"`
}

// importArchiveUpload godoc
// @Summary      Import a task archive uploaded in chunks (Purpose exercise-archive)
// @Description  The archive is read where its chunks lie. Without device image files the tasks are created at once:
// @Description  200 with the created tasks (the archive limits of the platform settings apply). With image files (`image: {file: …}`)
// @Description  the import runs in the background: 202 {JobID}; poll GET /exercises/import/jobs/{jobID}. One import
// @Description  per upload: asking again returns the same job (a failed one runs again). 409 72801/72802 when the
// @Description  platform image registry is not configured or failing; 409 70993 when the upload is not a complete
// @Description  task archive upload; 404 when the upload is absent, expired or another user's.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  importUploadRequest  true  "the upload and the archive password"
// @Success  200  {object}  response.Response{data=[]exerciseResponse}
// @Success  202  {object}  response.Response{data=importJobStarted}
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /exercises/import/upload [post]
func (h *Handler) importArchiveUpload(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req importUploadRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.AuthorizeImport(ctx, exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}, req.OwnerEventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	start, err := h.useCase.ImportUploadedArchive(ctx, exerciseUseCase.ImportUploadInput{UploadID: req.UploadID, Password: req.Password, CreatedBy: claims.UserID, OwnerEventID: req.OwnerEventID})
	if err != nil {
		abortArchiveError(ctx, err)
		return
	}
	if start.JobID != uuid.Nil {
		response.AbortWithDataStatus(ctx, http.StatusAccepted, importJobStarted{JobID: start.JobID})
		return
	}
	created := make([]exerciseResponse, 0, len(start.Exercises))
	for _, view := range start.Exercises {
		created = append(created, exerciseToResponse(view))
	}
	response.AbortWithData(ctx, created)
}

// importJobError is why a background import failed, as an error response's Status carries it: the API code, the
// message and the public context (e.g. reason, file, field). Never a secret.
type importJobError struct {
	Code    int            `json:"Code"`
	Message string         `json:"Message"`
	Context map[string]any `json:"Context,omitempty"`
}

// runningImport is one import of the caller that still runs.
type runningImport struct {
	JobID       string `json:"JobID"`
	Stage       string `json:"Stage"`
	ImagesDone  int    `json:"ImagesDone"`
	ImagesTotal int    `json:"ImagesTotal"`
	TasksDone   int    `json:"TasksDone"`
	TasksTotal  int    `json:"TasksTotal"`
	BytesPushed int64  `json:"BytesPushed"`
	BytesTotal  int64  `json:"BytesTotal"`
}

// runningImportJobs godoc
// @Summary      The caller's background task imports that still run
// @Description  So a page on another device, or with cleared storage, can follow them (GET /exercises/import/jobs/{jobID}).
// @Tags     exercises
// @Produce  json
// @Success  200  {object}  response.Response{data=[]runningImport}
// @Router   /exercises/import/jobs [get]
func (h *Handler) runningImportJobs(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	jobs, err := h.useCase.RunningArchiveImports(ctx, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]runningImport, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, runningImport{
			JobID: job.ID.String(), Stage: string(job.Stage),
			ImagesDone: job.Progress.ImagesDone, ImagesTotal: job.Progress.ImagesTotal,
			TasksDone: job.Progress.TasksDone, TasksTotal: job.Progress.TasksTotal,
			BytesPushed: job.Progress.BytesPushed, BytesTotal: job.Progress.BytesTotal,
		})
	}
	response.AbortWithData(ctx, out)
}

type importJobResponse struct {
	// State is running, done or failed.
	State string `json:"State"`
	// Stage is unpacking, images or tasks while running, then done or failed.
	Stage string `json:"Stage"`
	// The counts of the stage: what is done of what is known so far (a total is 0 until its stage is reached).
	ImagesDone  int `json:"ImagesDone"`
	ImagesTotal int `json:"ImagesTotal"`
	TasksDone   int `json:"TasksDone"`
	TasksTotal  int `json:"TasksTotal"`
	// BytesPushed of BytesTotal are the image bytes sent to the registry (counted per finished image).
	BytesPushed int64 `json:"BytesPushed"`
	BytesTotal  int64 `json:"BytesTotal"`
	// Exercises are the created tasks (done only).
	Exercises []exerciseResponse `json:"Exercises,omitempty"`
	// Error is why it failed (failed only).
	Error *importJobError `json:"Error,omitempty"`
}

// importJob godoc
// @Summary      State of a background task archive import
// @Description  running → done (Exercises: the created tasks) or failed (Error: Code, Message, Context). Nothing is
// @Description  created unless the whole import succeeded. Only the user who started it sees it (404 otherwise).
// @Tags     exercises
// @Produce  json
// @Param    jobID  path  string  true  "import job ID"
// @Success  200  {object}  response.Response{data=importJobResponse}
// @Failure  404  {object}  response.Response
// @Router   /exercises/import/jobs/{jobID} [get]
func (h *Handler) importJob(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("jobID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	job, err := h.useCase.ArchiveImportJob(ctx, id, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := importJobResponse{
		State: string(job.State), Stage: string(job.Stage),
		ImagesDone: job.Progress.ImagesDone, ImagesTotal: job.Progress.ImagesTotal,
		TasksDone: job.Progress.TasksDone, TasksTotal: job.Progress.TasksTotal,
		BytesPushed: job.Progress.BytesPushed, BytesTotal: job.Progress.BytesTotal,
	}
	if job.Error != nil {
		out.Error = &importJobError{Code: job.Error.Code, Message: job.Error.Message, Context: job.Error.Context}
	}
	for _, exerciseID := range job.ExerciseIDs {
		view, getErr := h.useCase.GetExercise(ctx, exerciseID)
		if errors.Is(getErr, exerciseModel.ErrExerciseNotFound.Err()) {
			continue // removed since the import
		}
		if getErr != nil {
			response.AbortWithError(ctx, getErr)
			return
		}
		out.Exercises = append(out.Exercises, exerciseToResponse(view))
	}
	response.AbortWithData(ctx, out)
}

// importContents expands the archive bytes and imports every exercise in them, all under lim.
func (h *Handler) importContents(ctx *gin.Context, createdBy uuid.UUID, ownerEventID *uuid.UUID, contents []byte, password string, lim exerciseModel.ArchiveLimits) {
	imports, err := exerciseUseCase.ExpandExerciseArchiveBundle(contents, password, lim)
	if err != nil {
		abortArchiveError(ctx, err)
		return
	}
	created := make([]exerciseResponse, 0, len(imports))
	for _, item := range imports {
		view, importErr := h.useCase.ImportExerciseArchive(ctx, exerciseUseCase.ImportExerciseInput{
			Archive: item.Archive, Password: item.Password, CreatedBy: createdBy, OwnerEventID: ownerEventID, Limits: lim,
		})
		if importErr != nil {
			response.AbortWithError(ctx, importErr)
			return
		}
		created = append(created, exerciseToResponse(view))
	}
	response.AbortWithData(ctx, created)
}

// abortArchiveError keeps a typed error (its code and public context reach the client; an internal one is answered
// generically); an untyped failure of the archive itself (a broken zip, a wrong password) stays a plain 400 with its
// text; any other untyped failure (storage, database) is an internal error whose text stays in the server log.
func abortArchiveError(ctx *gin.Context, failure error) {
	var typed appErr.Error
	switch {
	case errors.As(failure, &typed):
		response.AbortWithError(ctx, failure)
	case exerciseUseCase.IsArchiveFormatError(failure):
		response.AbortWithBadRequest(ctx, failure)
	default:
		response.AbortWithError(ctx, model.ErrPlatform.WithError(failure).WithMessage("Failed to import the archive").Err())
	}
}

func archiveFilename(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	name = strings.Trim(name, "-_")
	if name == "" {
		name = "exercise"
	}
	return filepath.Base(name) + ".cybericebox.zip"
}
