package event

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type eventStageResponse struct {
	LabRetentionMinutes *int32    `json:"LabRetentionMinutes" extensions:"x-nullable"`
	ID                  uuid.UUID `json:"ID"`
	Name                string    `json:"Name"`
	OpensAt             time.Time `json:"OpensAt"`
	ClosesAt            time.Time `json:"ClosesAt"`
	Returnable          bool      `json:"Returnable"`
	OpensWithEvent      bool      `json:"OpensWithEvent"`
	ClosesWithEvent     bool      `json:"ClosesWithEvent"`
	// State is computed from time: upcoming | open | closed.
	State string `json:"State"`
	// First and Last mark the stage’s place in time order. All stages have independent dates.
	First bool `json:"First"`
	Last  bool `json:"Last"`
	// DeployLeadMinutes is the lead the platform computes for the labs that open with this stage: their deploy
	// starts that long before OpensAt (0 when unknown).
	DeployLeadMinutes int `json:"DeployLeadMinutes"`
}

type createEventStageRequest struct {
	LabRetentionMinutes *int32    `json:"LabRetentionMinutes"`
	Name                string    `json:"Name"`
	OpensAt             time.Time `json:"OpensAt"`
	ClosesAt            time.Time `json:"ClosesAt"`
	Returnable          bool      `json:"Returnable"`
	OpensWithEvent      bool      `json:"OpensWithEvent"`
	ClosesWithEvent     bool      `json:"ClosesWithEvent"`
}

type updateEventStageRequest struct {
	LabRetentionMinutes eventUseCase.OptionalLimit `json:"LabRetentionMinutes" swaggertype:"integer"`
	Name                *string                    `json:"Name"`
	OpensAt             *time.Time                 `json:"OpensAt"`
	ClosesAt            *time.Time                 `json:"ClosesAt"`
	Returnable          *bool                      `json:"Returnable"`
	OpensWithEvent      *bool                      `json:"OpensWithEvent"`
	ClosesWithEvent     *bool                      `json:"ClosesWithEvent"`
	// CloseNow ends an open stage now («Закрити зараз»).
	CloseNow bool `json:"CloseNow"`
}

type setEventExerciseStageRequest struct {
	// StageID null puts the set back on the whole event.
	StageID *uuid.UUID `json:"StageID"`
}

func toEventStageResponse(v eventUseCase.EventStageView) eventStageResponse {
	return eventStageResponse{LabRetentionMinutes: v.LabRetentionMinutes, ID: v.ID, Name: v.Name, OpensAt: v.OpensAt, ClosesAt: v.ClosesAt, Returnable: v.Returnable, OpensWithEvent: v.OpensWithEvent, ClosesWithEvent: v.ClosesWithEvent, State: string(v.State), First: v.First, Last: v.Last, DeployLeadMinutes: v.DeployLeadMinutes}
}

// listEventStages godoc
// @Summary List the stages of an event with their computed state
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]eventStageResponse}
// @Router /events/{id}/manage/stages [get]
func (h *Handler) listEventStages(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListEventStages(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventStageResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toEventStageResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// createEventStage godoc
// @Summary Add a stage to an event
// @Description Each boundary is custom or explicitly bound to the scheduled event StartAt/FinishAt through OpensWithEvent/ClosesWithEvent. Boundaries follow future schedule edits until they occur. Windows cannot overlap or exceed the event schedule and platform availability window; other stages keep their dates. A finish binding requires a scheduled event finish.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body createEventStageRequest true "stage"
// @Success 200 {object} response.Response{data=eventStageResponse}
// @Router /events/{id}/manage/stages [post]
func (h *Handler) createEventStage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req createEventStageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.CreateEventStage(ctx, eventID, eventUseCase.CreateStageInput{LabRetentionMinutes: req.LabRetentionMinutes, Name: req.Name, OpensAt: req.OpensAt, ClosesAt: req.ClosesAt, Returnable: req.Returnable, OpensWithEvent: req.OpensWithEvent, ClosesWithEvent: req.ClosesWithEvent})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventStageResponse(view))
}

func parseStageParams(ctx *gin.Context) (eventID, stageID uuid.UUID, ok bool) {
	eventID, ok = parseEventID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	stageID, err := uuid.FromString(ctx.Param("stageID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, stageID, true
}

// updateEventStage godoc
// @Summary Update a stage by what its state allows
// @Description Upcoming: everything. Open: name, Returnable and ClosesAt (in the future), or CloseNow. Closed: only the name. Errors: stage_closed_locked, stage_opened_locked.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param stageID path string true "stage ID"
// @Param body body updateEventStageRequest true "changes"
// @Success 200 {object} response.Response{data=eventStageResponse}
// @Router /events/{id}/manage/stages/{stageID} [put]
func (h *Handler) updateEventStage(ctx *gin.Context) {
	eventID, stageID, ok := parseStageParams(ctx)
	if !ok {
		return
	}
	var req updateEventStageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdateEventStage(ctx, eventID, stageID, eventUseCase.UpdateStageInput{LabRetentionMinutes: req.LabRetentionMinutes, Name: req.Name, OpensAt: req.OpensAt, ClosesAt: req.ClosesAt, Returnable: req.Returnable, OpensWithEvent: req.OpensWithEvent, ClosesWithEvent: req.ClosesWithEvent, CloseNow: req.CloseNow})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventStageResponse(view))
}

// deleteEventStage godoc
// @Summary Delete an upcoming stage that holds no sets
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param stageID path string true "stage ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/stages/{stageID} [delete]
func (h *Handler) deleteEventStage(ctx *gin.Context) {
	eventID, stageID, ok := parseStageParams(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteEventStage(ctx, eventID, stageID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// setEventExerciseStage godoc
// @Summary Attach an exercise set to a stage (or to the whole event)
// @Description Before a stage opens everything is free; once it has opened nothing is moved out of it (stage_opened_locked) and a closed stage accepts nothing new (stage_closed_locked).
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param body body setEventExerciseStageRequest true "stage"
// @Success 200 {object} response.Response{data=eventExerciseResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/stage [put]
func (h *Handler) setEventExerciseStage(ctx *gin.Context) {
	eventID, exerciseID, _, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	var req setEventExerciseStageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.SetEventExerciseStage(ctx, eventID, exerciseID, req.StageID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventExerciseResponse(view))
}
