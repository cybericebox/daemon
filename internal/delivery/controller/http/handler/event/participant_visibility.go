package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

type setIndividualParticipantVisibilityRequest struct {
	Hidden bool `json:"Hidden"`
}

// @Summary Hide or show an individual participant's competitive unit
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param userID path string true "participant user ID"
// @Param body body setIndividualParticipantVisibilityRequest true "visibility"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/participants/{userID}/visibility [put]
func (h *Handler) setIndividualParticipantVisibility(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	var req setIndividualParticipantVisibilityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.SetIndividualParticipantHidden(ctx, eventID, userID, req.Hidden); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
