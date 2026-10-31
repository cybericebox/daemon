package event

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

type readinessResponse struct {
	// Ready: nothing to warn about (every task is published and every stand is ready).
	Ready   bool       `json:"Ready"`
	StartAt *time.Time `json:"StartAt"`
	Started bool       `json:"Started"`
	// UnpublishedTasks are the tasks of the active sets that participants do not see.
	UnpublishedTasks []readinessTaskResponse `json:"UnpublishedTasks"`
	// StandsApplicable: the event uses stands; StandsTotal / StandsReady count every team stand.
	StandsApplicable bool `json:"StandsApplicable"`
	StandsTotal      int  `json:"StandsTotal"`
	StandsReady      int  `json:"StandsReady"`
	// Stands are the stands that are not ready, with their labs (Status, Reason, WaitReason, WaitSince).
	Stands []standTeamResponse `json:"Stands"`
}

type readinessTaskResponse struct {
	ChallengeID uuid.UUID  `json:"ChallengeID"`
	Name        string     `json:"Name"`
	ExerciseID  uuid.UUID  `json:"ExerciseID"`
	StageID     *uuid.UUID `json:"StageID"`
}

// getReadiness godoc
// @Summary Readiness of the event: unpublished tasks and stands that are not ready
// @Description One look before the start: the tasks participants still do not see and the stands that are not ready, with what each lab waits for.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=readinessResponse}
// @Router /events/{id}/manage/readiness [get]
func (h *Handler) getReadiness(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetEventReadiness(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := readinessResponse{
		Ready:            view.Ready,
		StartAt:          view.StartAt,
		Started:          view.Started,
		UnpublishedTasks: make([]readinessTaskResponse, 0, len(view.UnpublishedTasks)),
		StandsApplicable: view.StandsApplicable,
		StandsTotal:      view.StandsTotal,
		StandsReady:      view.StandsReady,
		Stands:           make([]standTeamResponse, 0, len(view.Stands)),
	}
	for _, task := range view.UnpublishedTasks {
		out.UnpublishedTasks = append(
			out.UnpublishedTasks,
			readinessTaskResponse{
				ChallengeID: task.ChallengeID,
				Name:        task.Name,
				ExerciseID:  task.ExerciseID,
				StageID:     task.StageID,
			},
		)
	}
	for _, item := range view.Stands {
		out.Stands = append(out.Stands, toStandTeamResponse(item))
	}
	response.AbortWithData(ctx, out)
}
