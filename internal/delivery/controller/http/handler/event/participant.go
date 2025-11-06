package event

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
)

type IParticipantUseCase interface {
	GetEventParticipants(ctx context.Context, eventID uuid.UUID, page, pageSize int) ([]*eventModel.Participant, error)
	UpdateEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID, status int32) error
	DeleteEventParticipant(ctx context.Context, eventID, userID uuid.UUID) error
}

func (h *Handler) initParticipantAPIHandler(router *gin.RouterGroup) {
	participantsAPI := router.Group("participants", protection.RequireProtection())
	{
		participantsAPI.GET("", h.getParticipants)                          // get participants
		participantsAPI.PATCH("/:userID/status", h.updateParticipantStatus) // update participant status
		participantsAPI.DELETE("/:userID", h.deleteParticipant)             // delete participant
	}
}

func (h *Handler) getParticipants(ctx *gin.Context) {
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

	participants, err := h.useCase.GetEventParticipants(ctx, eventID, page, pageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, participants)
}

func (h *Handler) updateParticipantStatus(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	var inp eventModel.Participant

	if err = ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	err = h.useCase.UpdateEventParticipantStatus(ctx, eventID, userID, inp.ApprovalStatus)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) deleteParticipant(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	err = h.useCase.DeleteEventParticipant(ctx, eventID, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
