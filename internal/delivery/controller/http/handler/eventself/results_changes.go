package eventself

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type resultsChangesResponse struct {
	// Revision is the cursor for the next poll: the newest revision read,
	// including changes a frozen viewer does not get.
	Revision int64 `json:"Revision"`
	// Changes are the result changes after `since` this viewer may see.
	Changes []eventUseCase.LiveResultChangeView `json:"Changes"`
	// SnapshotRequired: the cursor is outside the change window or the
	// viewer's freeze state changed; reload GET /results once.
	SnapshotRequired bool `json:"SnapshotRequired"`
	// FreezeKey identifies the viewer's freeze state; send it back as
	// `freeze` so a change of it asks for a new snapshot.
	FreezeKey string `json:"FreezeKey"`
}

// resultsChanges godoc
// @Summary Poll result changes after a snapshot revision
// @Description The polling twin of /results/live for the results page: the same access rules, freeze and shared change window, one request per poll. Load GET /events/{id}/results first and pass its Revision as `since`. An unchanged answer is a 304 for the ETag the browser keeps.
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param since query integer true "Revision of the loaded snapshot, or the Revision of the previous poll"
// @Param freeze query string false "FreezeKey of the previous poll"
// @Success 200 {object} response.Response{data=resultsChangesResponse}
// @Router /events/{id}/results/changes [get]
func (h *Handler) resultsChanges(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	since, err := strconv.ParseInt(ctx.Query("since"), 10, 64)
	if err != nil || since < 0 {
		response.AbortWithBadRequest(ctx, fmt.Errorf("since must be a non-negative revision"))
		return
	}
	access := eventUseCase.ResultsAccess{Role: rbac.RolePublic}
	if claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); found {
		access.UserID, access.Role = &claims.UserID, claims.Role
	}
	replay, err := h.useCase.OpenLiveResults(eventID, access, false).Replay(ctx, since)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := resultsChangesResponse{Revision: replay.LastRevision, Changes: replay.Changes, SnapshotRequired: replay.SnapshotRequired, FreezeKey: replay.FreezeKey}
	if out.Changes == nil {
		out.Changes = []eventUseCase.LiveResultChangeView{}
	}
	if previous, sent := ctx.GetQuery("freeze"); sent && previous != replay.FreezeKey {
		out.SnapshotRequired = true
	}
	if out.SnapshotRequired {
		out.Revision, out.Changes = since, []eventUseCase.LiveResultChangeView{}
	}
	abortWithETagData(ctx, out)
}
