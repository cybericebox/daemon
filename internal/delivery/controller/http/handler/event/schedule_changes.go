package event

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type scheduleResponse struct {
	JoinPolicy       int16      `json:"JoinPolicy"`
	PublishAt        time.Time  `json:"PublishAt"`
	StartAt          time.Time  `json:"StartAt"`
	FinishAt         *time.Time `json:"FinishAt"`
	WithdrawAt       *time.Time `json:"WithdrawAt"`
	ManualFinishedAt *time.Time `json:"ManualFinishedAt"`
}

type scheduleChangeResponse struct {
	ID        uuid.UUID  `json:"ID"`
	ChangedAt time.Time  `json:"ChangedAt"`
	ChangedBy *uuid.UUID `json:"ChangedBy"`
	// ChangedByName is the manager's name or email. ByApplication false: the schedule changed outside the
	// application (a hand edit in the database); DBRole is then the database role that did it.
	ChangedByName string           `json:"ChangedByName"`
	ByApplication bool             `json:"ByApplication"`
	DBRole        string           `json:"DBRole"`
	Before        scheduleResponse `json:"Before"`
	After         scheduleResponse `json:"After"`
}

func toScheduleResponse(v eventUseCase.ScheduleView) scheduleResponse {
	return scheduleResponse{JoinPolicy: v.JoinPolicy, PublishAt: v.PublishAt, StartAt: v.StartAt, FinishAt: v.FinishAt, WithdrawAt: v.WithdrawAt, ManualFinishedAt: v.ManualFinishedAt}
}

// @Summary Who changed the event schedule, when, and from what to what
// @Description The latest 200 recorded changes of the schedule (publication, start, finish, withdrawal, manual finish, join policy), newest first. Every change is recorded, including one made outside the application.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]scheduleChangeResponse}
// @Router /events/{id}/manage/lifecycle/changes [get]
func (h *Handler) listScheduleChanges(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	changes, err := h.useCase.ListEventScheduleChanges(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]scheduleChangeResponse, 0, len(changes))
	for _, c := range changes {
		out = append(out, scheduleChangeResponse{ID: c.ID, ChangedAt: c.ChangedAt, ChangedBy: c.ChangedBy, ChangedByName: c.ChangedByName, ByApplication: c.ByApplication,
			DBRole: c.DBRole, Before: toScheduleResponse(c.Before), After: toScheduleResponse(c.After)})
	}
	response.AbortWithData(ctx, out)
}
