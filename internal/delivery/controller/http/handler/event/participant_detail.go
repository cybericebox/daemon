package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// participantDetailResponse is the list row plus what the detail view shows.
type participantDetailResponse struct {
	participantResponse
	// TeamRole is 0 for the captain, 1 for a member; null without a team.
	TeamRole *int16 `json:"TeamRole"`
	// JoinedVia is open, approval or invitation.
	JoinedVia string `json:"JoinedVia"`
	Attempts  int64  `json:"Attempts"`
	Solves    int64  `json:"Solves"`
}

func toParticipantDetailResponse(v eventUseCase.ParticipantDetailView) participantDetailResponse {
	out := participantDetailResponse{
		participantResponse: toParticipantResponse(v.ParticipantView), JoinedVia: v.JoinedVia, Attempts: v.Attempts, Solves: v.Solves,
	}
	if v.TeamRole != nil {
		role := int16(*v.TeamRole)
		out.TeamRole = &role
	}
	return out
}

// getParticipantDetail godoc
// @Summary  One participant of the event for the manager
// @Tags     events
// @Produce  json
// @Param    id      path  string  true  "event ID"
// @Param    userID  path  string  true  "user ID"
// @Success  200  {object}  response.Response{data=participantDetailResponse}
// @Failure  404  {object}  response.Response
// @Router   /events/{id}/manage/participants/{userID} [get]
func (h *Handler) getParticipantDetail(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.GetParticipantDetail(ctx, eventID, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantDetailResponse(v))
}
