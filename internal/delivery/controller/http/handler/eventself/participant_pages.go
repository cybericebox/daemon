package eventself

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// challengeSolves godoc
// @Summary List the teams that solved a challenge of the caller's board (one page)
// @Description Needs the board gates and event results access (under a freeze only solves before it, and results not published answer with the results-hidden error). TeamName is the public scoreboard name. Only visible teams are listed, plus the caller's own team (Own). Oldest solve first; FirstBlood marks the earliest solve among the visible teams. The cursor is the id of the last solve of the previous page (NextCursor); sort is ignored.
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param cursor query string false "NextCursor of the previous page"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.CursorPage[challengeSolveResponse]}
// @Router /events/{id}/teams/challenges/{challengeID}/solves [get]
func (h *Handler) challengeSolves(ctx *gin.Context) {
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
	params, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	page, err := h.useCase.ListChallengeSolves(ctx, eventID, claims.UserID, challengeID, params.Cursor, params.PageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, solvesPageResponse(page))
}

// ownTeamMembers godoc
// @Summary List the members of the caller's team (captain first)
// @Description Role: 0 captain, 1 member. DisplayName is the participant public name.
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]teamMemberResponse}
// @Router /events/{id}/teams/mine/members [get]
func (h *Handler) ownTeamMembers(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListOwnTeamMembers(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]teamMemberResponse, 0, len(items))
	for _, item := range items {
		out = append(out, teamMemberResponse{UserID: item.UserID, DisplayName: item.DisplayName, Role: int16(item.Role), Own: item.Own, Pending: item.Pending})
	}
	response.AbortWithData(ctx, out)
}

// participantAnswers godoc
// @Summary Read the caller's registration answers with the latest participant form
// @Tags events-self
// @Produce json
// @Success 200 {object} response.Response{data=participantAnswersResponse}
// @Router /events/self/participant-answers [get]
func (h *Handler) participantAnswers(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	v, err := h.useCase.GetOwnParticipantAnswers(ctx, tenant.EventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, toParticipantAnswersResponse(v))
}

// updateParticipantAnswers godoc
// @Summary Change the caller's editable registration answers (until the event finish)
// @Tags events-self
// @Accept json
// @Produce json
// @Param body body updateParticipantAnswersRequest true "answers"
// @Success 200 {object} response.Response{data=participantAnswersResponse}
// @Router /events/self/participant-answers [put]
func (h *Handler) updateParticipantAnswers(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req updateParticipantAnswersRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if req.Answers == nil {
		req.Answers = map[string]any{}
	}
	v, err := h.useCase.UpdateOwnParticipantAnswers(ctx, tenant.EventID, claims.UserID, req.Answers)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, toParticipantAnswersResponse(v))
}

func solvesPageResponse(page eventUseCase.ChallengeSolvesPage) pagination.CursorPage[challengeSolveResponse] {
	items := make([]challengeSolveResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, challengeSolveResponse{TeamName: item.TeamName, NameHidden: item.NameHidden, SolvedAt: item.SolvedAt, Own: item.Own, FirstBlood: item.FirstBlood})
	}
	return pagination.NewCursorPage(items, page.HasMore, page.Next, page.Total)
}
