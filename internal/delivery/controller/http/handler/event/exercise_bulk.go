package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// bulkExercisesRequest selects 1..200 of the event's exercises (sets) for one bulk action.
type bulkExercisesRequest struct {
	ExerciseIDs []uuid.UUID `json:"ExerciseIDs"`
}

type bulkExerciseVisibilityRequest struct {
	bulkExercisesRequest
	Published bool `json:"Published"`
}

// bulkExerciseScoringRequest: Override null gives every task the event's scoring.
type bulkExerciseScoringRequest struct {
	bulkExercisesRequest
	Override *updateScoringProfileRequest `json:"Override"`
}

// bulkExerciseDetachRequest: Confirm also removes sets teams already attempted (kept as detached evidence).
type bulkExerciseDetachRequest struct {
	bulkExercisesRequest
	Confirm bool `json:"Confirm"`
}

// bulkExerciseItemResponse is one exercise of a bulk action: Changed, or skipped with the error a single action
// would have answered (Code, Message).
type bulkExerciseItemResponse struct {
	ID      uuid.UUID `json:"ID"`
	Changed bool      `json:"Changed"`
	Code    int       `json:"Code,omitempty"`
	Message string    `json:"Message,omitempty"`
}

type bulkExerciseResponse struct {
	Changed int                        `json:"Changed"`
	Skipped int                        `json:"Skipped"`
	Results []bulkExerciseItemResponse `json:"Results"`
}

func writeBulkExerciseResults(ctx *gin.Context, results []eventUseCase.BulkExerciseResult, err error) {
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := bulkExerciseResponse{Results: make([]bulkExerciseItemResponse, 0, len(results))}
	for _, result := range results {
		item := bulkExerciseItemResponse{ID: result.ID, Changed: result.Err == nil}
		if result.Err != nil {
			item.Code, item.Message = response.PublicError(result.Err)
			out.Skipped++
		} else {
			out.Changed++
		}
		out.Results = append(out.Results, item)
	}
	response.AbortWithData(ctx, out)
}

// bulkSetEventExerciseVisibility godoc
// @Summary Show or hide 1..200 sets on the participant board; each set as the single action would
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body bulkExerciseVisibilityRequest true "sets and visibility"
// @Success 200 {object} response.Response{data=bulkExerciseResponse}
// @Router /events/{id}/manage/exercises/bulk/visibility [put]
func (h *Handler) bulkSetEventExerciseVisibility(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req bulkExerciseVisibilityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	results, err := h.useCase.BulkSetEventExerciseVisibility(ctx, eventID, req.ExerciseIDs, req.Published)
	writeBulkExerciseResults(ctx, results, err)
}

// bulkSetEventExerciseScoring godoc
// @Summary Give every task of 1..200 sets the same local scoring (null: the event's); each set as the single action would
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body bulkExerciseScoringRequest true "sets and scoring"
// @Success 200 {object} response.Response{data=bulkExerciseResponse}
// @Router /events/{id}/manage/exercises/bulk/scoring [put]
func (h *Handler) bulkSetEventExerciseScoring(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req bulkExerciseScoringRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var results []eventUseCase.BulkExerciseResult
	var err error
	if req.Override != nil {
		profile := req.Override.profile()
		results, err = h.useCase.BulkSetEventExerciseScoring(ctx, eventID, req.ExerciseIDs, &profile)
	} else {
		results, err = h.useCase.BulkSetEventExerciseScoring(ctx, eventID, req.ExerciseIDs, nil)
	}
	writeBulkExerciseResults(ctx, results, err)
}

// bulkDetachEventExercises godoc
// @Summary Remove 1..200 sets from the event; each set as the single detach would (Confirm for attempted sets)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body bulkExerciseDetachRequest true "sets"
// @Success 200 {object} response.Response{data=bulkExerciseResponse}
// @Router /events/{id}/manage/exercises/bulk/detach [post]
func (h *Handler) bulkDetachEventExercises(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !found {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req bulkExerciseDetachRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	results, err := h.useCase.BulkDetachEventExercises(ctx, eventID, req.ExerciseIDs, req.Confirm, claims.UserID)
	writeBulkExerciseResults(ctx, results, err)
}
