package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

// avatarRequestSlack is the room for the multipart framing around an avatar file.
const avatarRequestSlack = 64 << 10

// uploadAvatar godoc
// @Summary  Upload the authenticated user's avatar (multipart "file")
// @Tags     auth
// @Accept   multipart/form-data
// @Produce  json
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/account/avatar [post]
func (h *Handler) uploadAvatar(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	// Cap the raw body before the multipart form is read: the avatar limit plus the form framing.
	middleware.LimitBody(ctx, authUseCase.MaxAvatarBytes+avatarRequestSlack)
	fileHeader, err := ctx.FormFile("file")
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = f.Close() }()

	contentType := fileHeader.Header.Get("Content-Type")
	if err = h.useCase.UploadAvatar(
		ctx.Request.Context(),
		userID,
		f,
		fileHeader.Size,
		contentType,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// removeAvatar godoc
// @Summary  Remove the authenticated user's avatar
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/account/avatar [delete]
func (h *Handler) removeAvatar(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if err := h.useCase.RemoveAvatar(ctx.Request.Context(), userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// getAvatar godoc
// @Summary  Stream a user's avatar from storage (public proxy)
// @Tags     auth
// @Produce  octet-stream
// @Param    id   path  string  true  "User ID"
// @Success  200  {file}  binary
// @Failure  404  {object}  response.Response
// @Router   /auth/avatar/{id} [get]
func (h *Handler) getAvatar(ctx *gin.Context) {
	userID, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithNotFound(ctx)
		return
	}
	reader, contentType, err := h.useCase.GetAvatar(ctx.Request.Context(), userID)
	if err != nil {
		// Any failure (missing object, storage off) is a 404 for this public image route.
		response.AbortWithNotFound(ctx)
		return
	}
	defer func() { _ = reader.Close() }()
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Defense-in-depth for a user-influenced binary served on the auth origin:
	// forbid MIME sniffing, force inline non-document rendering, and sandbox.
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", "inline; filename=avatar")
	ctx.Header("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	// The picture URL carries a ?v=<ts> cache-buster that changes on every store,
	// so the content at a given URL never changes — cache it immutably.
	ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	// The global ContentMiddleware presets Content-Type: application/json and
	// DataFromReader does not overwrite an existing value; with nosniff above the
	// browser would refuse to render the image. Set the stored type explicitly.
	ctx.Header("Content-Type", contentType)
	ctx.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
