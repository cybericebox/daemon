package exercise

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

type (
	// bulkExercisesRequest is the selection of the catalog table (at most MaxBulkExercises).
	bulkExercisesRequest struct {
		IDs []uuid.UUID `json:"IDs" binding:"required,min=1,max=200"`
	}

	bulkAccessRequest struct {
		IDs         []uuid.UUID `json:"IDs" binding:"required,min=1,max=200"`
		AccessLevel string      `json:"AccessLevel" enums:"all,selected,own,none"`
		EventIDs    []uuid.UUID `json:"EventIDs"`
	}

	bulkItemResponse struct {
		ID uuid.UUID `json:"ID"`
		// Exercise is the changed card; null for a skipped task.
		Exercise *exerciseResponse `json:"Exercise"`
		// Code is the error code (Status.Code) that skipped the task; 0 when it changed.
		Code int `json:"Code"`
	}

	bulkResultResponse struct {
		Changed int                `json:"Changed"`
		Skipped int                `json:"Skipped"`
		Items   []bulkItemResponse `json:"Items"`
	}
)

// bulkArchive godoc
// @Summary  Archive the selected exercises, each by the rule of one exercise's archive
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  bulkExercisesRequest  true  "exercise IDs (1..200)"
// @Success  200  {object}  response.Response{data=bulkResultResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/bulk/archive [post]
func (h *Handler) bulkArchive(ctx *gin.Context) {
	h.bulkSetArchived(ctx, true)
}

// bulkUnarchive godoc
// @Summary  Return the selected exercises to the catalog, each by the rule of one exercise's unarchive
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  bulkExercisesRequest  true  "exercise IDs (1..200)"
// @Success  200  {object}  response.Response{data=bulkResultResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/bulk/unarchive [post]
func (h *Handler) bulkUnarchive(ctx *gin.Context) {
	h.bulkSetArchived(ctx, false)
}

func (h *Handler) bulkSetArchived(ctx *gin.Context, archived bool) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req bulkExercisesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	response.AbortWithData(ctx, bulkToResponse(ctx, h.useCase.BulkSetArchived(ctx, actor, req.IDs, archived)))
}

// bulkSetAccess godoc
// @Summary  Set the same access on the selected catalog exercises
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  bulkAccessRequest  true  "exercise IDs (1..200); all | selected (+EventIDs) | own | none"
// @Success  200  {object}  response.Response{data=bulkResultResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/bulk/access [put]
func (h *Handler) bulkSetAccess(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req bulkAccessRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	level, valid := exerciseUseCase.ParseAccessLevel(req.AccessLevel)
	if !valid {
		response.AbortWithBadRequest(ctx, errors.New("AccessLevel must be all, selected, own or none"))
		return
	}
	in := exerciseUseCase.SetAccessInput{AccessLevel: level, EventIDs: req.EventIDs, UpdatedBy: actor.UserID}
	response.AbortWithData(ctx, bulkToResponse(ctx, h.useCase.BulkSetAccess(ctx, actor, req.IDs, in)))
}

func bulkToResponse(ctx *gin.Context, results []exerciseUseCase.BulkResult) bulkResultResponse {
	out := bulkResultResponse{Items: make([]bulkItemResponse, 0, len(results))}
	for _, r := range results {
		item := bulkItemResponse{ID: r.ID}
		if r.Err == nil {
			card := exerciseToResponse(r.View)
			item.Exercise = &card
			out.Changed++
		} else {
			item.Code = bulkFailureCode(ctx, r)
			out.Skipped++
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// bulkFailureCode is the code a skipped task is reported with, as one task's route would answer it: a typed error's
// code; an internal failure only by its code (the text stays in the server log).
func bulkFailureCode(ctx *gin.Context, r exerciseUseCase.BulkResult) int {
	var typed appErr.Error
	if !errors.As(r.Err, &typed) {
		log.Error().Err(r.Err).Str("url", ctx.Request.URL.Path).Str("exercise", r.ID.String()).Msg("Bulk exercise action failed")
		return model.ErrPlatform.Err().StatusCode().FullCode()
	}
	code := typed.UnwrapNotInternalError().StatusCode()
	if code.IsInternal() {
		log.Error().Err(r.Err).Str("url", ctx.Request.URL.Path).Str("exercise", r.ID.String()).Msg("Bulk exercise action failed")
	}
	return code.FullCode()
}
