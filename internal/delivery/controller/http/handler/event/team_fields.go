package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// @Summary Get event team fields
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=participantFormResponse}
// @Router /events/{id}/manage/team-fields [get]
func (h *Handler) getTeamFields(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fields, err := h.useCase.GetTeamFields(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantFormResponse(fields))
}

// @Summary Configure event team fields
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param body body configureParticipantFormRequest true "team fields"
// @Success 200 {object} response.Response{data=participantFormResponse}
// @Router /events/{id}/manage/team-fields [put]
func (h *Handler) configureTeamFields(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req configureParticipantFormRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	fields, err := h.useCase.ConfigureTeamFields(ctx, eventID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantFormResponse(fields))
}
