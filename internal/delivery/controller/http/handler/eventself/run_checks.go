package eventself

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// challengeChecksRunner is the optional part of the use case that asks for a check of the caller's own task now.
type challengeChecksRunner interface {
	RunOwnChallengeChecks(ctx context.Context, eventID, userID, challengeID uuid.UUID) error
}

type runChecksResponse struct {
	// Asked: the laboratory was asked to check the task now. The result is not in this answer: the task is marked solved by
	// itself once its checks pass (read the board). Nothing of what is checked, expected or found is ever returned.
	Asked bool `json:"Asked"`
}

// runChecks godoc
// @Summary Ask for a check of the caller's own task now
// @Description Only for a task checked by the state of the lab that allows a manual check. A task that waits for other tasks answers 409 with the names of what it waits for (public context waits_for). At most 3 asks a minute per team and task (429 with Retry-After). The answer only says the request was recorded.
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Success 200 {object} response.Response{data=runChecksResponse}
// @Failure 409 {object} response.Response
// @Failure 429 {object} response.Response
// @Router /events/{id}/teams/challenges/{challengeID}/run-checks [post]
func (h *Handler) runChecks(ctx *gin.Context) {
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
	runner, ok := h.useCase.(challengeChecksRunner)
	if !ok {
		response.AbortWithError(ctx, infraModel.ErrInfrastructureUnavailable.Err())
		return
	}
	if err = runner.RunOwnChallengeChecks(ctx, eventID, claims.UserID, challengeID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, runChecksResponse{Asked: true})
}
