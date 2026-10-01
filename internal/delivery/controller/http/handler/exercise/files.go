package exercise

import (
	"errors"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// multipartOverheadSlack is added on top of the use case's own upload cap
// when bounding the raw HTTP request body: the multipart envelope (boundary
// markers, per-part headers, the non-file form fields) adds a small amount
// of overhead beyond the file's own bytes, and the body-level cap must not
// reject a legitimately-sized file for that overhead alone.
const multipartOverheadSlack = 1 << 20 // 1 MiB

type fileUploadResponse struct {
	FileID uuid.UUID `json:"FileID"`
	Name   string    `json:"Name"`
	Size   int64     `json:"Size"`
}

// uploadFile godoc
// @Summary  Upload an exercise attachment (multipart)
// @Tags     exercises
// @Accept   multipart/form-data
// @Produce  json
// @Param    file  formData  file  true  "attachment payload"
// @Success  200  {object}  response.Response{data=fileUploadResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/files [post]
func (h *Handler) uploadFile(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	// Cap the raw request body BEFORE gin parses the multipart form: FormFile
	// otherwise reads (and, past a size threshold, spills to temp files) the
	// entire body first, so an oversized upload would be fully absorbed
	// before the use case's own LimitReader ever runs. http.MaxBytesReader
	// makes the underlying Read calls fail past the cap, and ALSO closes the
	// connection so the client can't keep streaming.
	if err := h.useCase.AuthorizeFileUpload(ctx, exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	bodyCap := h.useCase.MaxUploadBytes() + multipartOverheadSlack
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, bodyCap)
	fh, err := ctx.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			// Same public fact as the use case's own post-hash size check
			// (ErrFileTooLarge, 400) — just caught earlier, at the HTTP
			// boundary, before the body is fully parsed. Documented
			// multi-site: both call sites report "the upload exceeded the
			// configured cap" and neither leaks anything security sensitive.
			response.AbortWithError(ctx, mediaModel.ErrFileTooLarge.Err())
			return
		}
		response.AbortWithBadRequest(ctx, err)
		return
	}
	f, err := fh.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = f.Close() }()

	uploaded, err := h.useCase.UploadFile(ctx, fh.Filename, fh.Header.Get("Content-Type"), f, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, fileUploadResponse{FileID: uploaded.ID, Name: uploaded.Name, Size: uploaded.SizeBytes})
}

// downloadFile godoc
// @Summary  Stream an exercise attachment
// @Tags     exercises
// @Produce  application/octet-stream
// @Param    fileID  path  string  true  "file ID"
// @Success  200  {file}  binary
// @Failure  400  {object}  response.Response
// @Router   /exercises/files/{fileID} [get]
func (h *Handler) downloadFile(ctx *gin.Context) {
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	rc, f, err := h.useCase.StreamFile(ctx, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = rc.Close() }()
	if err = h.useCase.AuthorizeFileDownload(ctx, actor, f); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	// f.Name is the raw client-supplied upload filename stored verbatim —
	// FormatMediaType quotes/escapes it (and RFC 2231-encodes non-ASCII) so a
	// `"` in the name cannot break out of the header's quoted-string.
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	// The stored content type is attacker-supplied too; attachment disposition
	// is the primary mitigation, nosniff is defense-in-depth (as in the avatar
	// route).
	ctx.Header("X-Content-Type-Options", "nosniff")
	contentType := f.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.DataFromReader(http.StatusOK, f.SizeBytes, contentType, rc, nil)
}
