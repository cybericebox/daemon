package eventself

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// @Summary Accept the caller's event invitation
// @Tags events-self
// @Success 200 {object} response.Response{data=joinInfoResponse}
// @Router /events/self/invitation/accept [post]
func (h *Handler) acceptInvitation(ctx *gin.Context) {
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
	view, err := h.useCase.AcceptParticipantInvitation(ctx, tenant.EventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toJoinInfoResponse(view))
}

// @Summary Decline the caller's pending event invitation
// @Tags events-self
// @Success 200 {object} response.Response
// @Router /events/self/invitation/decline [post]
func (h *Handler) declineInvitation(ctx *gin.Context) {
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
	if err := h.useCase.DeclineParticipantInvitation(ctx, tenant.EventID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// @Summary Set or clear the caller's pseudonym in the tenant event
// @Tags events-self
// @Accept json
// @Param body body setPseudonymRequest true "pseudonym; null clears it"
// @Success 200 {object} response.Response{data=participantNameResponse}
// @Router /events/self/pseudonym [put]
func (h *Handler) setPseudonym(ctx *gin.Context) {
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
	var req setPseudonymRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.SetOwnPseudonym(ctx, tenant.EventID, claims.UserID, req.Pseudonym)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, participantNameResponse{Pseudonym: view.Pseudonym, DisplayName: view.DisplayName})
}

// @Summary Update the editable extra fields of the caller's team (captain)
// @Tags events-self
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body updateTeamFieldsRequest true "changed field answers"
// @Success 200 {object} response.Response{data=ownTeamResponse}
// @Router /events/{id}/teams/{teamID}/fields [put]
func (h *Handler) updateTeamFields(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	teamID, err := uuid.FromString(ctx.Param("teamID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateTeamFieldsRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdateOwnTeamFields(ctx, eventID, teamID, claims.UserID, req.Fields)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toOwnTeamResponse(view))
}
