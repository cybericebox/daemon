package event

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// listSolutionAttempts godoc
// @Summary List privileged event solution attempts, including submitted and expected answers
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamId query string false "team ID"
// @Param participantId query string false "participant ID"
// @Param challengeId query string false "event challenge ID"
// @Param correct query bool false "correctness"
// @Param from query string false "RFC3339 inclusive lower time bound"
// @Param to query string false "RFC3339 exclusive upper time bound"
// @Param cursor query string false "last solution attempt ID; an unknown ID is 400"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.CursorPage[solutionAttemptResponse]}
// @Router /events/{id}/solution-attempts [get]
func (h *Handler) listSolutionAttempts(ctx *gin.Context) {
	h.writeSolutionAttempts(ctx, true)
}

// listManageSolutionAttempts godoc
// @Summary List event solution attempts for the event's managers
// @Description Viewers get the journal without submitted answers and expected flags (both null).
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamId query string false "team ID"
// @Param participantId query string false "participant ID"
// @Param challengeId query string false "event challenge ID"
// @Param correct query bool false "correctness"
// @Param from query string false "RFC3339 inclusive lower time bound"
// @Param to query string false "RFC3339 exclusive upper time bound"
// @Param cursor query string false "last solution attempt ID; an unknown ID is 400"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.CursorPage[solutionAttemptResponse]}
// @Router /events/{id}/manage/solution-attempts [get]
func (h *Handler) listManageSolutionAttempts(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	err := h.useCase.RequireManageEvent(ctx, eventID, claims.UserID)
	if err != nil && !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		response.AbortWithError(ctx, err)
		return
	}
	h.writeSolutionAttempts(ctx, err == nil)
}

// writeSolutionAttempts answers one journal page; answers and expected flags
// only when the caller may see them.
func (h *Handler) writeSolutionAttempts(ctx *gin.Context, withAnswers bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	page, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter, err := solutionAttemptsFilter(ctx, eventID)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter.Cursor, filter.PageSize = page.Cursor, page.PageSize
	result, err := h.useCase.ListSolutionAttempts(ctx, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]solutionAttemptResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toSolutionAttemptResponse(item, withAnswers))
	}
	response.AbortWithData(ctx, pagination.NewCursorPage(items, result.HasMore, result.NextCursor, result.Total))
}

// decideSolutionAttempt godoc
// @Summary Record a manual verdict for an event solution attempt
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param attemptID path string true "solution attempt ID"
// @Param body body decideSolutionAttemptRequest true "manual decision and required reason"
// @Success 200 {object} response.Response{data=solutionAttemptDecisionResponse}
// @Router /events/{id}/solution-attempts/{attemptID}/decision [patch]
func (h *Handler) decideSolutionAttempt(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	attemptID, err := uuid.FromString(ctx.Param("attemptID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req decideSolutionAttemptRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	decision, valid := challengeAttemptModel.ParseDecision(req.Decision)
	if !valid {
		response.AbortWithError(ctx, challengeAttemptModel.ErrDecisionInvalid.Err())
		return
	}
	v, err := h.useCase.DecideSolutionAttempt(ctx, eventID, attemptID, eventUseCase.DecideSolutionAttemptInput{Decision: decision, Reason: req.Reason, DecidedBy: claims.UserID})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toSolutionAttemptDecisionResponse(v))
}

// solutionAttemptsFilter reads the journal filters shared by the list and the
// CSV export.
func solutionAttemptsFilter(ctx *gin.Context, eventID uuid.UUID) (eventUseCase.ListSolutionAttemptsFilter, error) {
	filter := eventUseCase.ListSolutionAttemptsFilter{EventID: eventID}
	var err error
	if filter.TeamID, err = optionalUUIDQuery(ctx, "teamId"); err != nil {
		return filter, err
	}
	if filter.ParticipantID, err = optionalUUIDQuery(ctx, "participantId"); err != nil {
		return filter, err
	}
	if filter.ChallengeID, err = optionalUUIDQuery(ctx, "challengeId"); err != nil {
		return filter, err
	}
	if filter.Correct, err = optionalBoolQuery(ctx, "correct"); err != nil {
		return filter, err
	}
	if filter.FromAt, err = optionalTimeQuery(ctx, "from"); err != nil {
		return filter, err
	}
	filter.ToAt, err = optionalTimeQuery(ctx, "to")
	return filter, err
}

func optionalUUIDQuery(ctx *gin.Context, key string) (*uuid.UUID, error) {
	value := ctx.Query(key)
	if value == "" {
		return nil, nil
	}
	id, err := uuid.FromString(value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
func optionalBoolQuery(ctx *gin.Context, key string) (*bool, error) {
	value := ctx.Query(key)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
func optionalTimeQuery(ctx *gin.Context, key string) (*time.Time, error) {
	value := ctx.Query(key)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
