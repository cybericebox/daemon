package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type inviteParticipantsRequest struct {
	Entries []struct {
		Email     string `json:"Email"`
		FirstName string `json:"FirstName"`
		LastName  string `json:"LastName"`
		// Fields prefill the participant form answers (CSV import).
		Fields map[string]any `json:"Fields"`
	} `json:"Entries" binding:"required"`
}

// @Summary Invite event participants by email
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param body body inviteParticipantsRequest true "email addresses"
// @Success 200 {object} response.Response{data=[]eventUseCase.ParticipantInvitationResult}
// @Router /events/{id}/manage/participants/invitations [post]
func (h *Handler) inviteParticipants(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req inviteParticipantsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	entries := make([]eventUseCase.ParticipantInvitationInput, 0, len(req.Entries))
	for _, entry := range req.Entries {
		entries = append(entries, eventUseCase.ParticipantInvitationInput{Email: entry.Email, FirstName: entry.FirstName, LastName: entry.LastName, Fields: entry.Fields})
	}
	var results []eventUseCase.ParticipantInvitationResult
	var err error
	if rawTeamID := ctx.Param("teamID"); rawTeamID != "" {
		teamID, parseErr := uuid.FromString(rawTeamID)
		if parseErr != nil {
			response.AbortWithBadRequest(ctx, parseErr)
			return
		}
		results, err = h.useCase.InviteTeamMembers(ctx, eventID, teamID, claims.UserID, entries)
	} else {
		results, err = h.useCase.InviteParticipants(ctx, eventID, claims.UserID, entries)
	}
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, results)
}

func parseParticipantUserID(ctx *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, userID, true
}

// @Summary Resend a pending participant invitation email
// @Tags events
// @Param id path string true "event ID"
// @Param userID path string true "invited user ID"
// @Success 200 {object} response.Response{data=invitationResendResponse}
// @Router /events/{id}/manage/participants/{userID}/invitation/resend [post]
func (h *Handler) resendInvitation(ctx *gin.Context) {
	eventID, userID, ok := parseParticipantUserID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	result, err := h.useCase.ResendParticipantInvitation(ctx, eventID, userID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, invitationResendResponse{UserID: result.UserID, Email: result.Email, InvitationSentAt: result.SentAt})
}

// @Summary Revoke a pending participant invitation
// @Tags events
// @Param id path string true "event ID"
// @Param userID path string true "invited user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/participants/{userID}/invitation [delete]
func (h *Handler) revokeInvitation(ctx *gin.Context) {
	eventID, userID, ok := parseParticipantUserID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := h.useCase.RevokeParticipantInvitation(ctx, eventID, userID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
