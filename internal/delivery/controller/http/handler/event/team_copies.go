package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type teamCopiesResponse struct {
	// Total counts the team copies of the set, Outdated those that differ from the task now.
	Total    int                    `json:"Total"`
	Outdated int                    `json:"Outdated"`
	Items    []teamCopyDiffResponse `json:"Items"`
}

type teamCopyDiffResponse struct {
	TeamID        uuid.UUID `json:"TeamID"`
	TeamName      string    `json:"TeamName"`
	ChallengeID   uuid.UUID `json:"ChallengeID"`
	ChallengeName string    `json:"ChallengeName"`
	// Differs lists what differs: name, description, difficulty, attachments, placeholders, hints.
	Differs []string `json:"Differs"`
	// Solved copies keep their pinned content and are not synced.
	Solved bool `json:"Solved"`
}

func toTeamCopiesResponse(v eventUseCase.TeamCopiesView) teamCopiesResponse {
	out := teamCopiesResponse{
		Total:    v.Total,
		Outdated: v.Outdated,
		Items:    make([]teamCopyDiffResponse, 0, len(v.Items)),
	}
	for _, item := range v.Items {
		out.Items = append(
			out.Items,
			teamCopyDiffResponse{
				TeamID:        item.TeamID,
				TeamName:      item.TeamName,
				ChallengeID:   item.ChallengeID,
				ChallengeName: item.ChallengeName,
				Differs:       item.Differs,
				Solved:        item.Solved,
			},
		)
	}
	return out
}

func parseEventExerciseIDs(ctx *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, exerciseID, true
}

// getTeamCopies godoc
// @Summary Team copies of a set's tasks that differ from the task now
// @Description Compares every team copy with the task of the version the set is pinned to (for the variant the team plays) and lists the copies that differ: text, attachments, hints.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Success 200 {object} response.Response{data=teamCopiesResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/team-copies [get]
func (h *Handler) getTeamCopies(ctx *gin.Context) {
	eventID, exerciseID, ok := parseEventExerciseIDs(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetTeamCopies(ctx, eventID, exerciseID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTeamCopiesResponse(view))
}

// syncTeamCopies godoc
// @Summary Bring the differing team copies of a set up to date
// @Description Refreshes text, attachments and hints of the differing copies from the pinned version and marks them updated on the board. The variant and the flag stay; solved copies are left alone. Returns what was updated.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Success 200 {object} response.Response{data=teamCopiesResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/team-copies/sync [post]
func (h *Handler) syncTeamCopies(ctx *gin.Context) {
	eventID, exerciseID, ok := parseEventExerciseIDs(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.SyncTeamCopies(ctx, eventID, exerciseID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTeamCopiesResponse(view))
}
