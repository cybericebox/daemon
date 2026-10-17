package exercise

import (
	"errors"
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
	appErr "github.com/cybericebox/daemon/pkg/err"
)

type exportExercisesRequest struct {
	IDs            []uuid.UUID `json:"IDs" binding:"required,min=1,max=100"`
	IncludeSecrets bool        `json:"IncludeSecrets"`
	Password       string      `json:"Password"`
}

// export godoc
// @Summary  Export selected exercises as one ZIP archive (several: one folder per exercise)
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
	items := make([]exerciseUseCase.NamedArchive, 0, len(req.IDs))
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
		items = append(items, exerciseUseCase.NamedArchive{Name: exercise.Name, Archive: archive})
	}
	if len(items) == 1 {
		serveArchive(ctx, archiveFilename(items[0].Name), items[0].Archive)
		return
	}
	password := ""
	if req.IncludeSecrets {
		password = req.Password
	}
	// One zip, one folder per task, one root manifest.json.
	bundle, err := exerciseUseCase.BundleExerciseArchives(items, password)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	serveArchive(ctx, "exercises.cybericebox.zip", bundle)
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
		abortArchiveError(ctx, err)
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

// abortArchiveError keeps a typed archive error (its code and public context reach the client); an untyped
// one (a broken zip, a size limit) stays a plain 400.
func abortArchiveError(ctx *gin.Context, failure error) {
	var typed appErr.Error
	if errors.As(failure, &typed) {
		response.AbortWithError(ctx, failure)
		return
	}
	response.AbortWithBadRequest(ctx, failure)
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
