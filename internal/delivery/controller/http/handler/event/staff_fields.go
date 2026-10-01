package event

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type staffChangeResponse struct {
	Keys      []string   `json:"Keys"`
	ActorID   *uuid.UUID `json:"ActorID"`
	ActorName string     `json:"ActorName"`
	At        time.Time  `json:"At"`
}

type staffFieldsResponse struct {
	Values map[string]any       `json:"Values"`
	Change *staffChangeResponse `json:"Change"`
}

type updateStaffFieldsRequest struct {
	// Values maps staff-only field keys to their new value; an empty value
	// clears the field, keys left out stay as they are.
	Values map[string]any `json:"Values" binding:"required"`
}

func toStaffFieldsResponse(v eventUseCase.StaffFieldsView) staffFieldsResponse {
	out := staffFieldsResponse{Values: v.Values}
	if out.Values == nil {
		out.Values = map[string]any{}
	}
	if v.Change != nil {
		keys := v.Change.Keys
		if keys == nil {
			keys = []string{}
		}
		out.Change = &staffChangeResponse{Keys: keys, ActorID: v.Change.ActorID, ActorName: v.Change.ActorName, At: v.Change.At}
	}
	return out
}

// @Summary Staff-only field values of a participant and who last changed them
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param userID path string true "participant user ID"
// @Success 200 {object} response.Response{data=staffFieldsResponse}
// @Router /events/{id}/manage/participants/{userID}/staff-fields [get]
func (h *Handler) getParticipantStaffFields(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetParticipantStaffFields(ctx, eventID, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStaffFieldsResponse(view))
}

// @Summary Save staff-only field values of a participant
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param userID path string true "participant user ID"
// @Param body body updateStaffFieldsRequest true "staff-only values"
// @Success 200 {object} response.Response{data=staffFieldsResponse}
// @Router /events/{id}/manage/participants/{userID}/staff-fields [put]
func (h *Handler) updateParticipantStaffFields(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	var req updateStaffFieldsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdateParticipantStaffFields(ctx, eventID, userID, claims.UserID, req.Values)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStaffFieldsResponse(view))
}

// @Summary Staff-only field values of a team and who last changed them
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Success 200 {object} response.Response{data=staffFieldsResponse}
// @Router /events/{id}/manage/teams/{teamID}/staff-fields [get]
func (h *Handler) getTeamStaffFields(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetTeamStaffFields(ctx, eventID, teamID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStaffFieldsResponse(view))
}

// @Summary Save staff-only field values of a team
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body updateStaffFieldsRequest true "staff-only values"
// @Success 200 {object} response.Response{data=staffFieldsResponse}
// @Router /events/{id}/manage/teams/{teamID}/staff-fields [put]
func (h *Handler) updateTeamStaffFields(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	var req updateStaffFieldsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdateTeamStaffFields(ctx, eventID, teamID, claims.UserID, req.Values)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStaffFieldsResponse(view))
}
