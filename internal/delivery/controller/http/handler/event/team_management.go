package event

import (
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type createManagedTeamRequest struct {
	Name      string         `json:"Name"`
	CaptainID uuid.UUID      `json:"CaptainID"`
	Fields    map[string]any `json:"Fields"`
}

type updateManagedTeamRequest struct {
	Name   string `json:"Name"`
	Hidden bool   `json:"Hidden"`
	// Fields omitted keeps the team's extra field answers.
	Fields map[string]any `json:"Fields"`
}

func parseManagedTeamID(ctx *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	teamID, err := uuid.FromString(ctx.Param("teamID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, teamID, true
}

// @Summary Read-only team profile for organizers: roster, captain, status, answers and results
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Success 200 {object} response.Response{data=teamProfileResponse}
// @Router /events/{id}/manage/teams/{teamID} [get]
func (h *Handler) getTeamProfile(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	profile, err := h.useCase.GetTeamProfile(ctx, eventID, teamID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := teamProfileResponse{Team: toTeamResponse(profile.Team)}
	if profile.Results != nil {
		results := toManageResultsTeamResponse(*profile.Results)
		out.Results = &results
	}
	response.AbortWithData(ctx, out)
}

// @Summary Create an event team as moderator
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param body body createManagedTeamRequest true "team fields"
// @Success 200 {object} response.Response{data=teamResponse}
// @Router /events/{id}/manage/teams [post]
func (h *Handler) createManagedTeam(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req createManagedTeamRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	team, err := h.useCase.CreateManagedTeam(ctx, eventID, eventUseCase.CreateManagedTeamInput{Name: req.Name, CaptainID: req.CaptainID, Fields: req.Fields})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTeamResponse(team))
}

// @Summary Update an event team as moderator
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body updateManagedTeamRequest true "team fields"
// @Success 200 {object} response.Response{data=teamResponse}
// @Router /events/{id}/manage/teams/{teamID} [put]
func (h *Handler) updateManagedTeam(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	var req updateManagedTeamRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	team, err := h.useCase.UpdateManagedTeam(ctx, eventID, teamID, eventUseCase.UpdateManagedTeamInput{Name: req.Name, Hidden: req.Hidden, Fields: req.Fields})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTeamResponse(team))
}

// @Summary Delete an event team as moderator
// @Tags events
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/teams/{teamID} [delete]
func (h *Handler) deleteManagedTeam(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteManagedTeam(ctx, eventID, teamID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// assignParticipantToTeam godoc
// @Summary Assign an event participant to a team
// @Tags events
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param userID path string true "participant user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/teams/{teamID}/members/{userID} [post]
func (h *Handler) assignParticipantToTeam(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.AssignParticipantToTeam(ctx, eventID, teamID, userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// @Summary Remove a non-captain participant from an event team
// @Tags events
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param userID path string true "participant user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/teams/{teamID}/members/{userID} [delete]
func (h *Handler) removeParticipantFromTeam(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.RemoveParticipantFromTeam(ctx, eventID, teamID, userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// @Summary Transfer event team captaincy as moderator
// @Tags events
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param userID path string true "new captain participant user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/teams/{teamID}/captain/{userID} [put]
func (h *Handler) transferManagedTeamCaptaincy(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.TransferManagedTeamCaptaincy(ctx, eventID, teamID, userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// setTeamAdmission godoc
// @Summary Set the moderator's manual admission of an event team
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body setTeamAdmissionRequest true "manual admission"
// @Success 200 {object} response.Response{data=teamResponse}
// @Router /events/{id}/manage/teams/{teamID}/admission [put]
func (h *Handler) setTeamAdmission(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	var req setTeamAdmissionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	team, err := h.useCase.SetTeamAdmission(ctx, eventID, teamID, req.AdmittedManually)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTeamResponse(team))
}

// setTeamHidden godoc
// @Summary Hide an event team from the results or show it again
// @Description Presentation only: hidden teams are absent from the scoreboard, live screen, solve counters and solver lists, and do not lower other teams' dynamic points. The moderators team is always hidden and answers 403.
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body setTeamHiddenRequest true "hidden flag"
// @Success 200 {object} response.Response{data=teamResponse}
// @Router /events/{id}/manage/teams/{teamID}/hidden [put]
func (h *Handler) setTeamHidden(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	var req setTeamHiddenRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	team, err := h.useCase.SetTeamHidden(ctx, eventID, teamID, req.Hidden)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTeamResponse(team))
}

// getListColumns godoc
// @Summary Get the shared extra-field column layout of a moderation list
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param list path string true "participants or teams"
// @Success 200 {object} response.Response{data=listColumnsDTO}
// @Router /events/{id}/manage/list-columns/{list} [get]
func (h *Handler) getListColumns(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	list := ctx.Param("list")
	columns, err := h.useCase.GetListColumns(ctx, eventID, list)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toListColumnsDTO(list, columns))
}

// putListColumns godoc
// @Summary Replace the shared extra-field column layout of a moderation list
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param list path string true "participants or teams"
// @Param body body listColumnsDTO true "ordered columns"
// @Success 200 {object} response.Response{data=listColumnsDTO}
// @Router /events/{id}/manage/list-columns/{list} [put]
func (h *Handler) putListColumns(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req listColumnsDTO
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	list := ctx.Param("list")
	columns := make([]eventUseCase.ListColumnView, 0, len(req.Columns))
	for _, column := range req.Columns {
		columns = append(columns, eventUseCase.ListColumnView{Key: column.Key, Visible: column.Visible})
	}
	saved, err := h.useCase.PutListColumns(ctx, eventID, list, columns, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toListColumnsDTO(list, saved))
}

// formManagedTeam godoc
// @Summary Form an event team as a moderator
// @Description Closes the roster for good without the minimum team size check; for a team that can never reach it. From then on nobody switches teams and the team gets its tasks.
// @Tags events
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/teams/{teamID}/form [post]
func (h *Handler) formManagedTeam(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := h.useCase.FormManagedTeam(ctx, eventID, teamID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
