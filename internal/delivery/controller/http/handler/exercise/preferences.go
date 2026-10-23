package exercise

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// catalogColumns godoc
// @Summary  The caller's catalog table columns: their order and which are shown; null until arranged once
// @Tags     exercises
// @Produce  json
// @Success  200  {object}  response.Response{data=[]exerciseUseCase.CatalogColumn}
// @Router   /exercises/preferences/catalog-columns [get]
func (h *Handler) catalogColumns(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	columns, err := h.useCase.CatalogColumns(ctx, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, columns)
}

// setCatalogColumns godoc
// @Summary  Saves the caller's catalog table columns (at most 32 distinct column names, in order)
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  []exerciseUseCase.CatalogColumn  true  "the columns in order"
// @Success  200  {object}  response.Response{data=[]exerciseUseCase.CatalogColumn}
// @Failure  400  {object}  response.Response
// @Router   /exercises/preferences/catalog-columns [put]
func (h *Handler) setCatalogColumns(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var columns []exerciseUseCase.CatalogColumn
	if err := ctx.ShouldBindJSON(&columns); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := exerciseUseCase.ValidateCatalogColumns(columns); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.SetCatalogColumns(ctx, actor, columns); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, columns)
}
