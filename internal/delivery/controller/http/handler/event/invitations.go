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
	// SendEmails false creates the same invitations without sending any email
	// (people accept on the event site). Omitted means send (the default).
	SendEmails *bool `json:"SendEmails"`
}

// inviteParticipants godoc
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
	noEmail := req.SendEmails != nil && !*req.SendEmails
	var results []eventUseCase.ParticipantInvitationResult
	var err error
	if rawTeamID := ctx.Param("teamID"); rawTeamID != "" {
		teamID, parseErr := uuid.FromString(rawTeamID)
		if parseErr != nil {
			response.AbortWithBadRequest(ctx, parseErr)
			return
		}
		results, err = h.useCase.InviteTeamMembers(ctx, eventID, teamID, claims.UserID, entries, noEmail)
	} else {
		results, err = h.useCase.InviteParticipants(ctx, eventID, claims.UserID, entries, noEmail)
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

// resendInvitation godoc
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

// revokeInvitation godoc
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

type changeInvitationEmailRequest struct {
	Email string `json:"Email" binding:"required"`
	// SendEmails false moves the invitation without sending any email. Omitted means send (the default).
	SendEmails *bool `json:"SendEmails"`
}

// changeInvitationEmail godoc
// @Summary Correct the email of a pending participant invitation
// @Description Moves the pending invitation (team, captaincy and prefilled answers included) to the account of the new address and sends a new email with a new link; the old link stops working. The same address just re-sends.
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param userID path string true "invited user ID"
// @Param body body changeInvitationEmailRequest true "new email"
// @Success 200 {object} response.Response{data=eventUseCase.ParticipantInvitationResult}
// @Router /events/{id}/manage/participants/{userID}/invitation/email [put]
func (h *Handler) changeInvitationEmail(ctx *gin.Context) {
	eventID, userID, ok := parseParticipantUserID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req changeInvitationEmailRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	noEmail := req.SendEmails != nil && !*req.SendEmails
	result, err := h.useCase.ChangeParticipantInvitationEmail(
		ctx,
		eventID,
		userID,
		claims.UserID,
		req.Email,
		noEmail,
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, result)
}

// acceptInvitationFor godoc
// @Summary Accept a pending participant invitation on behalf of the invited person
// @Description Organizers are not time-limited: the registration window, publication and a formed team do not stop it; the team size still applies.
// @Tags events
// @Param id path string true "event ID"
// @Param userID path string true "invited user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/participants/{userID}/invitation/accept [post]
func (h *Handler) acceptInvitationFor(ctx *gin.Context) {
	eventID, userID, ok := parseParticipantUserID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if _, err := h.useCase.AcceptParticipantInvitationFor(
		ctx,
		eventID,
		userID,
		claims.UserID,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// moveParticipantToTeam godoc
// @Summary Move a team member to another team
// @Description The participant leaves their current team for teamID. Organizers are not time-limited (formed teams and a started event do not stop it). The captain must hand over the captaincy first; the target team needs a free seat.
// @Tags events
// @Param id path string true "event ID"
// @Param userID path string true "participant user ID"
// @Param teamID path string true "target team ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/participants/{userID}/team/{teamID} [put]
func (h *Handler) moveParticipantToTeam(ctx *gin.Context) {
	eventID, userID, ok := parseParticipantUserID(ctx)
	if !ok {
		return
	}
	teamID, err := uuid.FromString(ctx.Param("teamID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.MoveParticipantToTeam(ctx, eventID, userID, teamID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
