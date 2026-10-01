package event

import (
	"errors"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// maxAnswerFileRequestBytes caps the multipart body before it is parsed: the
// platform maximum of a file question plus the multipart envelope.
const maxAnswerFileRequestBytes = eventFormModel.MaxAnswerFileMB<<20 + 1<<20

type answerFileResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
}

// uploadManagedAnswerFile godoc
// @Summary  Upload a file for a team «Файл» question on behalf of a team
// @Tags     events
// @Accept   multipart/form-data
// @Produce  json
// @Param    id     path      string  true  "event ID"
// @Param    file   formData  file    true  "file"
// @Param    scope  formData  string  true  "participant or team"
// @Param    field  formData  string  true  "question key"
// @Success  200  {object}  response.Response{data=answerFileResponse}
// @Router   /events/{id}/manage/answer-files [post]
func (h *Handler) uploadManagedAnswerFile(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxAnswerFileRequestBytes)
	header, err := ctx.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.AbortWithError(ctx, eventModel.ErrAnswerFileTooLarge.Err())
			return
		}
		response.AbortWithBadRequest(ctx, err)
		return
	}
	file, err := header.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = file.Close() }()
	uploaded, err := h.useCase.UploadAnswerFile(ctx.Request.Context(), eventID, claims.UserID, eventFormModel.AnswerScope(ctx.PostForm("scope")), ctx.PostForm("field"), header.Filename, file)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, answerFileResponse{ID: uploaded.ID, Name: uploaded.Name, Size: uploaded.Size, ContentType: uploaded.ContentType})
}

// downloadManagedAnswerFile godoc
// @Summary  Download a file attached to a participant or team answer
// @Tags     events
// @Produce  application/octet-stream
// @Param    id      path  string  true  "event ID"
// @Param    fileID  path  string  true  "file ID"
// @Success  200  {file}  binary
// @Router   /events/{id}/manage/answer-files/{fileID} [get]
func (h *Handler) downloadManagedAnswerFile(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	reader, file, err := h.useCase.StreamManagedAnswerFile(ctx.Request.Context(), eventID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Header("Cache-Control", "private, no-store")
	ctx.DataFromReader(http.StatusOK, file.Size, file.ContentType, reader, nil)
}
