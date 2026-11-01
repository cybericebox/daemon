package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
)

// getTablePrefs godoc
// @Summary  Get the current user's saved view of a table (null when none)
// @Tags     auth
// @Produce  json
// @Param    tableKey  path      string  true  "table key, up to 64 of [a-z0-9._-]"
// @Success  200  {object}  response.Response{data=tablePrefsModel.Prefs}
// @Failure  400  {object}  response.Response
// @Router   /auth/account/table-prefs/{tableKey} [get]
func (h *Handler) getTablePrefs(ctx *gin.Context) {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	prefs, err := h.useCase.GetTablePrefs(
		ctx.Request.Context(),
		claims.UserID,
		ctx.Param("tableKey"),
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, prefs)
}

// saveTablePrefs godoc
// @Summary  Save the current user's view of a table
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    tableKey  path      string                 true  "table key, up to 64 of [a-z0-9._-]"
// @Param    body      body      tablePrefsModel.Prefs  true  "the view"
// @Success  200  {object}  response.Response{data=tablePrefsModel.Prefs}
// @Failure  400  {object}  response.Response
// @Router   /auth/account/table-prefs/{tableKey} [put]
func (h *Handler) saveTablePrefs(ctx *gin.Context) {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	var req tablePrefsModel.Prefs
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	saved, err := h.useCase.SaveTablePrefs(
		ctx.Request.Context(),
		claims.UserID,
		ctx.Param("tableKey"),
		req,
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, saved)
}

// resetTablePrefs godoc
// @Summary  Forget the current user's view of a table (back to the default)
// @Tags     auth
// @Produce  json
// @Param    tableKey  path      string  true  "table key, up to 64 of [a-z0-9._-]"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /auth/account/table-prefs/{tableKey} [delete]
func (h *Handler) resetTablePrefs(ctx *gin.Context) {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if err := h.useCase.ResetTablePrefs(
		ctx.Request.Context(),
		claims.UserID,
		ctx.Param("tableKey"),
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
