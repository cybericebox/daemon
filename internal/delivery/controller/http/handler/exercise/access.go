package exercise

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

const exerciseAccessKey = "exerciseAccess"

func actorFrom(ctx *gin.Context) (exerciseUseCase.Actor, bool) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return exerciseUseCase.Actor{}, false
	}
	return exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}, true
}

// authorize applies the exercise policy for one action on the :id exercise
// and keeps the outcome for the handler (published-only readers).
func (h *Handler) authorize(action exerciseUseCase.Action) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, ok := parseExerciseID(ctx)
		if !ok {
			return
		}
		actor, ok := actorFrom(ctx)
		if !ok {
			return
		}
		access, err := h.useCase.AuthorizeExercise(ctx, actor, id, action)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		ctx.Set(exerciseAccessKey, access)
	}
}

func accessFrom(ctx *gin.Context) exerciseUseCase.Access {
	if value, ok := ctx.Get(exerciseAccessKey); ok {
		if access, ok := value.(exerciseUseCase.Access); ok {
			return access
		}
	}
	return exerciseUseCase.Access{}
}

// maxEventFilter caps the event filter (a table filter, not a bulk export).
const maxEventFilter = 100

// applyVisibilityQuery reads the W4 list filters (scope, event,
// infrastructure). event may repeat and/or be comma-separated; duplicates
// collapse.
func applyVisibilityQuery(ctx *gin.Context, f *exerciseUseCase.ExercisesFilter) bool {
	f.Scope = ctx.Query("scope")
	f.Infrastructure = ctx.Query("infrastructure")
	seen := map[uuid.UUID]bool{}
	for _, value := range ctx.QueryArray("event") {
		for raw := range strings.SplitSeq(value, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			id, err := uuid.FromString(raw)
			if err != nil {
				response.AbortWithBadRequest(ctx, err)
				return false
			}
			if !seen[id] {
				seen[id] = true
				f.EventIDs = append(f.EventIDs, id)
			}
		}
	}
	if len(f.EventIDs) > maxEventFilter {
		response.AbortWithBadRequest(ctx, fmt.Errorf("at most %d events", maxEventFilter))
		return false
	}
	return true
}

// access godoc
// @Summary  What the caller may do in the exercises app
// @Tags     exercises
// @Produce  json
// @Success  200  {object}  response.Response{data=accessSummaryResponse}
// @Router   /exercises/access [get]
func (h *Handler) access(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	summary, err := h.useCase.GetAccessSummary(ctx, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := accessSummaryResponse{IsAdmin: summary.IsAdmin, CanCreateCatalog: summary.CanCreateCatalog, CanPublish: summary.CanPublish,
		CanDelete: summary.CanDelete, CanExport: summary.CanExport, Events: make([]accessEventResponse, 0, len(summary.Events))}
	for _, e := range summary.Events {
		out.Events = append(out.Events, accessEventResponse(e))
	}
	response.AbortWithData(ctx, out)
}

// setAccess godoc
// @Summary  Set which events may use a catalog exercise
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string            true  "exercise ID"
// @Param    body  body  setAccessRequest  true  "all | selected (+EventIDs) | own | none (no event may use it)"
// @Success  200  {object}  response.Response{data=exerciseResponse}
// @Router   /exercises/{id}/access [put]
func (h *Handler) setAccess(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req setAccessRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	level, valid := exerciseUseCase.ParseAccessLevel(req.AccessLevel)
	if !valid {
		response.AbortWithBadRequest(ctx, errors.New("AccessLevel must be all, selected, own or none"))
		return
	}
	v, err := h.useCase.SetExerciseAccess(ctx, actor, id, exerciseUseCase.SetAccessInput{AccessLevel: level, EventIDs: req.EventIDs, UpdatedBy: actor.UserID})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, exerciseToResponse(v))
}

// propose godoc
// @Summary  Propose a published event exercise to the platform catalog
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string          true  "exercise ID"
// @Param    body  body  proposeRequest  false "note for the reviewer"
// @Success  200  {object}  response.Response{data=proposalResponse}
// @Router   /exercises/{id}/proposals [post]
func (h *Handler) propose(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req proposeRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	p, err := h.useCase.ProposeExercise(ctx, actor, id, req.Note)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, proposalToResponse(p))
}

// listProposals godoc
// @Summary  Catalog proposals (admin review)
// @Tags     exercises
// @Produce  json
// @Param    status  query  string  false  "pending | approved | rejected"
// @Success  200  {object}  response.Response{data=[]proposalResponse}
// @Router   /exercises/proposals [get]
func (h *Handler) listProposals(ctx *gin.Context) {
	items, err := h.useCase.ListProposals(ctx, ctx.Query("status"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]proposalResponse, 0, len(items))
	for _, p := range items {
		out = append(out, proposalToResponse(p))
	}
	response.AbortWithData(ctx, out)
}

func parseProposalID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("proposalID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

// approveProposal godoc
// @Summary  Approve a proposal: copy the exercise into the catalog
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    proposalID  path  string                  true  "proposal ID"
// @Param    body        body  approveProposalRequest  true  "name, access level, events"
// @Success  200  {object}  response.Response{data=proposalResponse}
// @Router   /exercises/proposals/{proposalID}/approve [post]
func (h *Handler) approveProposal(ctx *gin.Context) {
	proposalID, ok := parseProposalID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req approveProposalRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	level, valid := exerciseUseCase.ParseAccessLevel(req.AccessLevel)
	if !valid {
		response.AbortWithError(ctx, exerciseModel.ErrExerciseAccessInvalid.Err())
		return
	}
	p, err := h.useCase.ApproveProposal(ctx, actor, proposalID, exerciseUseCase.ApproveProposalInput{Name: req.Name, AccessLevel: level, EventIDs: req.EventIDs, Note: req.Note})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, proposalToResponse(p))
}

// rejectProposal godoc
// @Summary  Reject a proposal
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    proposalID  path  string                 true  "proposal ID"
// @Param    body        body  rejectProposalRequest  false "note"
// @Success  200  {object}  response.Response{data=proposalResponse}
// @Router   /exercises/proposals/{proposalID}/reject [post]
func (h *Handler) rejectProposal(ctx *gin.Context) {
	proposalID, ok := parseProposalID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req rejectProposalRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	p, err := h.useCase.RejectProposal(ctx, actor, proposalID, req.Note)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, proposalToResponse(p))
}
