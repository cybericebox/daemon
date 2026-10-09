package eventself

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"strconv"
)

type manualLabRequest struct {
	Revision       string    `json:"Revision"`
	IdempotencyKey uuid.UUID `json:"IdempotencyKey"`
}
type labManualCommands interface {
	StopOwnLab(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, eventUseCase.ManualLabInput) (eventUseCase.ParticipantLabView, error)
	RestartOwnLab(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, eventUseCase.ManualLabInput) (eventUseCase.ParticipantLabView, error)
}

// stopLab godoc
// @Summary Stop the caller's unresolved progressive laboratory
// @Tags events-self
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param labID path string true "canonical laboratory ID"
// @Param body body manualLabRequest true "current revision and idempotency key"
// @Success 200 {object} response.Response{data=labLifecycleResponse}
// @Router /events/{id}/teams/labs/{labID}/stop [post]
func (h *Handler) stopLab(ctx *gin.Context) { h.manualLab(ctx, false) }

// restartLab godoc
// @Summary Restart the caller's retained manually stopped progressive laboratory
// @Tags events-self
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param labID path string true "canonical laboratory ID"
// @Param body body manualLabRequest true "current revision and idempotency key"
// @Success 200 {object} response.Response{data=labLifecycleResponse}
// @Router /events/{id}/teams/labs/{labID}/restart [post]
func (h *Handler) restartLab(ctx *gin.Context) { h.manualLab(ctx, true) }
func (h *Handler) manualLab(ctx *gin.Context, restart bool) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	labID, err := uuid.FromString(ctx.Param("labID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	var in manualLabRequest
	if err = ctx.ShouldBindJSON(&in); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	revision, err := strconv.ParseInt(in.Revision, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != in.Revision {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	commands, ok := h.useCase.(labManualCommands)
	if !ok {
		response.AbortWithError(ctx, infraModel.ErrInfrastructureUnavailable.Err())
		return
	}
	input := eventUseCase.ManualLabInput{ExpectedRevision: revision, IdempotencyKey: in.IdempotencyKey}
	var view eventUseCase.ParticipantLabView
	if restart {
		view, err = commands.RestartOwnLab(ctx, eventID, claims.UserID, labID, input)
	} else {
		view, err = commands.StopOwnLab(ctx, eventID, claims.UserID, labID, input)
	}
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, labLifecycleResponse{Lab: labview.ParticipantLab(&view)})
}
