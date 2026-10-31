// Package user is the HTTP delivery of the platform users: the table, the card and the bulk actions.
package user

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/err"
)

type (
	userParticipationResponse struct {
		EventID   uuid.UUID  `json:"EventID"`
		EventName string     `json:"EventName"`
		EventTag  string     `json:"EventTag"`
		TeamID    *uuid.UUID `json:"TeamID"`
		TeamName  string     `json:"TeamName"`
		// Role: participant (no team yet), captain, member, or owner / manager / viewer of the event.
		Role string `json:"Role"`
		// Status of the participation: 1 pending, 2 approved, 3 rejected; 2 for an event the user manages.
		Status int16 `json:"Status"`
	}

	bulkUsersRequest struct {
		UserIDs []uuid.UUID `json:"UserIDs"`
	}

	bulkUserResultResponse struct {
		UserID uuid.UUID `json:"UserID"`
		Ok     bool      `json:"Ok"`
		// Code is the catalog code of the refusal (empty when Ok); Error its English text.
		Code  string `json:"Code,omitempty"`
		Error string `json:"Error,omitempty"`
	}

	bulkUsersResponse struct {
		Results []bulkUserResultResponse `json:"Results"`
	}
)

// parseUserFilters reads and validates the extra filters of the user table.
func parseUserFilters(ctx *gin.Context) (string, string, int, error) {
	emailConfirmed, signIn := ctx.Query("emailConfirmed"), ctx.Query("signIn")
	inactiveDays := 0
	if emailConfirmed != "" && emailConfirmed != "true" && emailConfirmed != "false" {
		return "", "", 0, errors.New("emailConfirmed must be true or false")
	}
	switch signIn {
	case "", "password", "google", "passkey":
	default:
		return "", "", 0, errors.New("signIn must be password, google or passkey")
	}
	if raw := ctx.Query("inactiveDays"); raw != "" {
		days, parseErr := strconv.Atoi(raw)
		if parseErr != nil || days < 1 || days > 3650 {
			return "", "", 0, errors.New("inactiveDays must be between 1 and 3650")
		}
		inactiveDays = days
	}
	return emailConfirmed, signIn, inactiveDays, nil
}

// listParticipations godoc
// @Summary  The events a user takes part in or manages
// @Tags     users
// @Produce  json
// @Param    userID  path      string  true  "user UUID"
// @Success  200  {object}  response.Response{data=[]userParticipationResponse}
// @Router   /users/{userID}/participations [get]
func (h *Handler) listParticipations(ctx *gin.Context) {
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	items, listErr := h.useCase.ListUserParticipations(ctx.Request.Context(), userID)
	if listErr != nil {
		response.AbortWithError(ctx, listErr)
		return
	}
	out := make([]userParticipationResponse, 0, len(items))
	for _, item := range items {
		out = append(
			out,
			userParticipationResponse{
				EventID:   item.EventID,
				EventName: item.EventName,
				EventTag:  item.EventTag,
				TeamID:    item.TeamID,
				TeamName:  item.TeamName,
				Role:      item.Role,
				Status:    item.Status,
			},
		)
	}
	response.AbortWithData(ctx, out)
}

// endSessions godoc
// @Summary  Sign a user out everywhere
// @Tags     users
// @Produce  json
// @Param    userID  path      string  true  "user UUID"
// @Success  200  {object}  response.Response
// @Router   /users/{userID}/sessions/end [post]
func (h *Handler) endSessions(ctx *gin.Context) {
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	if endErr := h.useCase.EndUserSessions(ctx.Request.Context(), userID); endErr != nil {
		response.AbortWithError(ctx, endErr)
		return
	}
	response.AbortWithSuccess(ctx)
}

// resendInvitation godoc
// @Summary  Send the invitation of an unfinished registration again
// @Description Only for status incomplete; the old link stops working.
// @Tags     users
// @Produce  json
// @Param    userID  path      string  true  "user UUID"
// @Success  200  {object}  response.Response
// @Router   /users/{userID}/invitation/resend [post]
func (h *Handler) resendInvitation(ctx *gin.Context) {
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	if sendErr := h.useCase.ResendUserInvitation(ctx.Request.Context(), userID); sendErr != nil {
		response.AbortWithError(ctx, sendErr)
		return
	}
	response.AbortWithSuccess(ctx)
}

// bulkResend godoc
// @Summary  Send the invitations of many unfinished registrations again
// @Description 1 to 200 users; one result per user in request order; a refusal for one never stops the others. Same for /users/bulk/block, /users/bulk/unblock and /users/bulk/end-sessions.
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    body  body      bulkUsersRequest  true  "users"
// @Success  200  {object}  response.Response{data=bulkUsersResponse}
// @Router   /users/bulk/resend-invitation [post]
func (h *Handler) bulkResend(ctx *gin.Context) {
	h.runBulk(ctx, authUseCase.BulkResendInvitation, rbac.PermUsersInvite)
}

// bulkBlock godoc
// @Summary  Block many users
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    body  body      bulkUsersRequest  true  "users"
// @Success  200  {object}  response.Response{data=bulkUsersResponse}
// @Router   /users/bulk/block [post]
func (h *Handler) bulkBlock(ctx *gin.Context) {
	h.runBulk(ctx, authUseCase.BulkBlock, rbac.PermUsersStatusWrite)
}

// bulkUnblock godoc
// @Summary  Unblock many users
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    body  body      bulkUsersRequest  true  "users"
// @Success  200  {object}  response.Response{data=bulkUsersResponse}
// @Router   /users/bulk/unblock [post]
func (h *Handler) bulkUnblock(ctx *gin.Context) {
	h.runBulk(ctx, authUseCase.BulkUnblock, rbac.PermUsersStatusWrite)
}

// bulkEndSessions godoc
// @Summary  End the sessions of many users
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    body  body      bulkUsersRequest  true  "users"
// @Success  200  {object}  response.Response{data=bulkUsersResponse}
// @Router   /users/bulk/end-sessions [post]
func (h *Handler) bulkEndSessions(ctx *gin.Context) {
	h.runBulk(ctx, authUseCase.BulkEndSessions, rbac.PermUsersStatusWrite)
}

// runBulk serves POST /users/bulk/{action}: every user gets the checks of the single action, one result each, one
// audit entry each.
func (h *Handler) runBulk(ctx *gin.Context, action string, permission rbac.Permission) {
	var req bulkUsersRequest
	if bindErr := ctx.ShouldBindJSON(&req); bindErr != nil {
		response.AbortWithBadRequest(ctx, bindErr)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	results, actionErr := h.useCase.BulkUserAction(ctx.Request.Context(), action, req.UserIDs)
	if actionErr != nil {
		response.AbortWithError(ctx, actionErr)
		return
	}
	audit.SetTarget(ctx, "bulk:"+action)
	out := bulkUsersResponse{Results: make([]bulkUserResultResponse, 0, len(results))}
	for _, result := range results {
		item := bulkUserResultResponse{UserID: result.UserID, Ok: result.Err == nil}
		status := http.StatusOK
		if result.Err != nil {
			item.Code, item.Error, status = describeBulkError(result.Err)
		}
		out.Results = append(out.Results, item)
		h.auditBulkItem(ctx, claims.UserID, permission, result.UserID, status)
	}
	response.AbortWithData(ctx, out)
}

// auditBulkItem writes the audit entry of one user of a bulk request.
func (h *Handler) auditBulkItem(
	ctx *gin.Context,
	actor uuid.UUID,
	permission rbac.Permission,
	target uuid.UUID,
	status int,
) {
	_ = h.useCase.RecordAdminAction(ctx.Request.Context(), adminAuditUseCase.Entry{
		ActorID: actor, Permission: string(permission), Method: ctx.Request.Method,
		Route: ctx.FullPath(), ResponseStatus: status, Target: "user:" + target.String(),
	})
}

// describeBulkError turns an error into the catalog code, the English text and the HTTP status the single action
// would have answered; an unknown fault is a generic internal error.
func describeBulkError(e error) (string, string, int) {
	var coded err.Error
	if errors.As(e, &coded) {
		inner := coded.UnwrapNotInternalError()
		if !inner.StatusCode().IsInternal() {
			full := inner.StatusCode().FullCode()
			return strconv.Itoa(full), inner.StatusCode().
					Message(),
				inner.StatusCode().
					HTTPCode()
		}
	}
	return "", "Internal server error", http.StatusInternalServerError
}
