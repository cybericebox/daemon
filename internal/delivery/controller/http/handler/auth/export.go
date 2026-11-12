package auth

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// exportMyData godoc
// @Summary  Download everything the platform stores about the current user (JSON file)
// @Description  Needs a sign-in from the last minutes (403 ErrAuthReauthRequired otherwise) and is limited to one download per 10 minutes (429 with RetryAfterSeconds). The answer is the file itself, not the usual envelope.
// @Tags     auth
// @Produce  json
// @Success  200  {object}  userModel.DataExport
// @Failure  403  {object}  response.Response
// @Failure  429  {object}  response.Response
// @Router   /auth/account/export [get]
func (h *Handler) exportMyData(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	data, err := h.useCase.ExportMyData(ctx.Request.Context(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	body, err := json.MarshalIndent(
		data,
		"",
		"  ",
	) //nolint:musttag // the file format is the PascalCase field names of DataExport
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header(
		"Content-Disposition",
		`attachment; filename="cyber-ice-box-my-data-`+data.ExportedAt.Format(
			"2006-01-02",
		)+`.json"`,
	)
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Data(http.StatusOK, "application/json; charset=utf-8", body)
	ctx.Abort()
}
