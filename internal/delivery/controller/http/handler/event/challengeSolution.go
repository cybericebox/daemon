package event

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
)

type IChallengeSolutionUseCase interface {
	GetEventChallengeSolutionAttempts(ctx context.Context, eventID uuid.UUID, page, pageSize int) (
		[]*eventModel.TeamChallengeSolutionAttempt,
		error,
	)
	UpdateEventChallengeSolutionAttempt(
		ctx context.Context,
		solutionAttempt eventModel.TeamChallengeSolutionAttempt,
	) error
}

func (h *Handler) initChallengeSolutionAPIHandler(router *gin.RouterGroup) {
	solutionAPI := router.Group("solutions")
	{
		solutionAPI.GET("", h.getSolutions)
		solutionAPI.PATCH(":solutionID/status", h.updateSolutionStatus)
	}
}

func (h *Handler) getSolutions(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	page, pageSize, err := tools.GetPaginationParams(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	solutions, err := h.useCase.GetEventChallengeSolutionAttempts(ctx, eventID, page, pageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, solutions)
}

func (h *Handler) updateSolutionStatus(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	solutionID, err := uuid.FromString(ctx.Param("solutionID"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	var inp eventModel.TeamChallengeSolutionAttempt
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	inp.EventID = eventID
	inp.ID = solutionID

	if err = h.useCase.UpdateEventChallengeSolutionAttempt(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
