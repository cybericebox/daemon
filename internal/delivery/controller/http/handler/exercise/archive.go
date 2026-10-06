package exercise

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

type exportExercisesRequest struct {
	IDs            []uuid.UUID `json:"IDs" binding:"required,min=1,max=100"`
	IncludeSecrets bool        `json:"IncludeSecrets"`
	Password       string      `json:"Password"`
}

// export godoc
// @Summary  Export selected exercises as standard ZIP archives
// @Tags     exercises
// @Accept   json
// @Produce  application/zip
// @Param    body  body  exportExercisesRequest  true  "selection and secret option"
// @Success  200  {file}  binary
// @Failure  400  {object}  response.Response
// @Router   /exercises/export [post]
func (h *Handler) export(ctx *gin.Context) {
	var req exportExercisesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if req.IncludeSecrets && strings.TrimSpace(req.Password) == "" {
		response.AbortWithBadRequest(ctx, fmt.Errorf("a password is required when exporting secrets"))
		return
	}
	archives := make(map[string][]byte, len(req.IDs))
	usedNames := map[string]int{}
	for _, id := range req.IDs {
		exercise, err := h.useCase.GetExercise(ctx, id)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		archive, err := h.useCase.ExportExerciseArchive(ctx, id, exerciseUseCase.ExportOptions{IncludeSecrets: req.IncludeSecrets, Password: req.Password})
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		name := archiveFilename(exercise.Name)
		usedNames[name]++
		if usedNames[name] > 1 {
			name = strings.TrimSuffix(name, ".cybericebox.zip") + fmt.Sprintf("-%d.cybericebox.zip", usedNames[name])
		}
		archives[name] = archive
	}
	if len(archives) == 1 {
		for name, archive := range archives {
			serveArchive(ctx, name, archive)
			return
		}
	}
	bundleFiles := map[string][]byte{}
	for name, archive := range archives {
		folder := strings.TrimSuffix(name, ".cybericebox.zip")
		if err := addExerciseFolder(bundleFiles, folder, archive, req.IncludeSecrets, req.Password); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
	}
	bundle, err := exerciseUseCase.BuildArchiveV1(bundleFiles)
	if req.IncludeSecrets && err == nil {
		bundle, err = exerciseUseCase.BuildProtectedArchiveV1(bundleFiles, req.Password)
	}
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	serveArchive(ctx, "exercises.cybericebox.zip", bundle)
}

// addExerciseFolder expands an individual archive into a directory in the
// catalogue bundle. A normal export is copied verbatim. For a protected
// export, public exercise metadata and attachments remain browseable while the
// one JSON payload containing secret values becomes a separately password-
// encrypted file; plaintext secret values never enter the outer ZIP.
func addExerciseFolder(bundle map[string][]byte, folder string, archive []byte, includesSecrets bool, password string) error {
	files, err := exerciseUseCase.ReadArchiveV1(bytes.NewReader(archive))
	if includesSecrets {
		files, err = exerciseUseCase.ReadProtectedArchiveV1(archive, password)
	}
	if err != nil {
		return err
	}
	for path, body := range files {
		bundle[folder+"/"+path] = body
	}
	return nil
}

// importArchive godoc
// @Summary  Import an exercise ZIP archive
// @Tags     exercises
// @Accept   multipart/form-data
// @Produce  json
// @Param    archive   formData  file    true   "exercise ZIP"
// @Param    password  formData  string  false  "archive password when it contains secrets"
// @Success  200  {object}  response.Response{data=[]exerciseResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/import [post]
func (h *Handler) importArchive(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	middleware.LimitBody(ctx, 129<<20)
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
	contents, err := readArchiveUpload(input)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	h.importContents(ctx, claims.UserID, contents, ctx.PostForm("password"))
}

type importUploadedRequest struct {
	// FileID is the file a chunked upload produced (POST /exercises/uploads/{uploadID}/complete).
	FileID   uuid.UUID `json:"FileID" binding:"required"`
	Password string    `json:"Password"`
}

// importUploadedArchive godoc
// @Summary  Import an exercise ZIP archive that went up in chunks
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  importUploadedRequest  true  "the uploaded file and the archive password"
// @Success  200  {object}  response.Response{data=[]exerciseResponse}
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
	contents, err := readArchiveUpload(reader)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	h.importContents(ctx, claims.UserID, contents, req.Password)
}

// importContents expands the archive bytes and imports every exercise in them.
func (h *Handler) importContents(ctx *gin.Context, createdBy uuid.UUID, contents []byte, password string) {
	imports, err := exerciseUseCase.ExpandExerciseArchiveBundle(contents, password)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	created := make([]exerciseResponse, 0, len(imports))
	for _, item := range imports {
		view, importErr := h.useCase.ImportExerciseArchive(ctx, exerciseUseCase.ImportExerciseInput{
			Archive: item.Archive, Password: item.Password, CreatedBy: createdBy,
		})
		if importErr != nil {
			response.AbortWithError(ctx, importErr)
			return
		}
		created = append(created, exerciseToResponse(view))
	}
	response.AbortWithData(ctx, created)
}

func readArchiveUpload(file io.Reader) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(file, (128<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > 128<<20 {
		return nil, fmt.Errorf("archive exceeds 128 MiB")
	}
	return contents, nil
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

func serveArchive(ctx *gin.Context, filename string, contents []byte) {
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Data(http.StatusOK, "application/zip", contents)
}
