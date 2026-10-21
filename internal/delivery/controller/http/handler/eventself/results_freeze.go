package eventself

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
)

// resultsFreeze godoc
// @Summary Read visible event freeze state without downloading scores
// @Description Uses the same viewer, event visibility and withdrawal gates as the results page. Authored banner content is returned only while the freeze applies to this viewer.
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=resultsFreezeResponse}
// @Failure 403 {object} response.Response
// @Failure 404 {object} response.Response
// @Router /events/{id}/results/freeze [get]
func (h *Handler) resultsFreeze(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetResultsFreeze(ctx, eventID, resultsAccess(ctx, eventID))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	abortWithETagData(ctx, toResultsFreezeResponse(view))
}
