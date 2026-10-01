package eventself

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// openChallenge godoc
// @Summary Record that the caller opened a task of the team's board
// @Description Analytics beacon for time on task. At most one record per user and task per minute; repeats are accepted and ignored.
// @Tags events-self
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Success 204
// @Router /events/{id}/teams/challenges/{challengeID}/open [post]
func (h *Handler) openChallenge(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	if err = h.useCase.OpenOwnChallenge(ctx, eventID, claims.UserID, challengeID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithNoContent(ctx)
}
