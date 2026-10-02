package template

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// multipartOverheadSlack is added on top of the use case's raw upload cap when
// bounding the HTTP request body: the multipart envelope adds a little
// overhead beyond the file's own bytes (as in the exercise files handler).
const multipartOverheadSlack = 1 << 20 // 1 MiB

// emailImageURLPrefix is the public path of an uploaded template image.
const emailImageURLPrefix = "/api/notifications/templates/email/images/"

// imageCacheControl: an uploaded file never changes under its id.
const imageCacheControl = "private, max-age=31536000, immutable"

type emailImageUploadResponse struct {
	FileID uuid.UUID `json:"FileID"`
	Url    string    `json:"Url"`
}

// uploadEmailImage godoc
// @Summary      Upload an email template image (multipart)
// @Description  Accepts PNG, JPEG or GIF (sniffed; SVG rejected). Images wider than 1200 px are downscaled; the processed image must be at most 300 KB.
// @Tags         notifications
// @Accept       multipart/form-data
// @Produce      json
// @Param        file  formData  file  true  "image payload"
// @Success      200  {object}  response.Response{data=emailImageUploadResponse}
// @Failure      400  {object}  response.Response
// @Router       /notifications/templates/email/images [post]
func (h *Handler) uploadEmailImage(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	// Cap the raw body BEFORE gin parses the multipart form (see exercise
	// files.go): FormFile would otherwise absorb an oversized upload first.
	bodyCap := h.useCase.MaxEmailImageUploadBytes() + multipartOverheadSlack
	middleware.LimitBody(ctx, bodyCap)
	fh, err := ctx.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			// Documented multi-site: same public fact as the use case's own
			// raw-size check, caught at the HTTP boundary.
			response.AbortWithError(ctx, notificationModel.ErrTemplateImageTooLarge.WithError(err).Err())
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

	uploaded, err := h.useCase.UploadEmailImage(ctx, f, fh.Filename, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailImageUploadResponse{FileID: uploaded.ID, Url: emailImageURLPrefix + uploaded.ID.String()})
}

// streamEmailImage godoc
// @Summary  Stream an uploaded email template image
// @Tags     notifications
// @Produce  image/png,image/jpeg
// @Param    fileID  path  string  true  "file ID"
// @Success  200  {file}  binary
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /notifications/templates/email/images/{fileID} [get]
func (h *Handler) streamEmailImage(ctx *gin.Context) {
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	rc, f, err := h.useCase.StreamEmailImage(ctx, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = rc.Close() }()

	ctx.Header("Content-Disposition", "inline")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Cache-Control", imageCacheControl)
	ctx.DataFromReader(http.StatusOK, f.SizeBytes, f.ContentType, rc, nil)
}

// brandLogo godoc
// @Summary  Platform brand logo used by the email logo block
// @Tags     notifications
// @Produce  image/png
// @Success  200  {file}  binary
// @Router   /notifications/templates/email/brand/logo [get]
func (h *Handler) brandLogo(ctx *gin.Context) {
	ctx.Header("Content-Disposition", "inline")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Cache-Control", "private, max-age=86400")
	ctx.Data(http.StatusOK, branding.LogoContentType, branding.LogoPNG())
}
