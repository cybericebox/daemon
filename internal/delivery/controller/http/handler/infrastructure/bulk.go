// Package infrastructure is the HTTP delivery of the admin infrastructure tables and actions.
package infrastructure

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	infrastructureUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
	"github.com/cybericebox/daemon/pkg/err"
)

// Bounds of one bulk request: a recreation is heavy, an end is cheap.
const (
	maxBulkStands   = 50
	maxBulkTestLabs = infrastructureUseCase.MaxBulkTestLabs
)

var errInvalidSort = errors.New("sortBy or sortDir is not valid")

type (
	bulkStandItem struct {
		EventID uuid.UUID `json:"EventID"`
		TeamID  uuid.UUID `json:"TeamID"`
	}

	bulkRecreateRequest struct {
		Items []bulkStandItem `json:"Items"`
	}

	bulkTerminateRequest struct {
		IDs []uuid.UUID `json:"IDs"`
	}

	// bulkResult is the outcome for one item: Ok, or the catalog Code and the English Error text of the refusal.
	bulkResult struct {
		EventID uuid.UUID `json:"EventID,omitempty"`
		TeamID  uuid.UUID `json:"TeamID,omitempty"`
		ID      uuid.UUID `json:"ID,omitempty"`
		Ok      bool      `json:"Ok"`
		Code    string    `json:"Code,omitempty"`
		Error   string    `json:"Error,omitempty"`
	}

	bulkResponse struct {
		Results []bulkResult `json:"Results"`
	}
)

// validSort accepts an empty sort or one of the columns with asc / desc (an empty direction is desc).
func validSort(by, dir string, columns ...string) bool {
	if by == "" {
		return dir == ""
	}
	if dir != "" && dir != "asc" && dir != "desc" {
		return false
	}
	for _, column := range columns {
		if by == column {
			return true
		}
	}
	return false
}

func testLabsFilter(q testLabsQuery) (infrastructureUseCase.TestLabsFilter, error) {
	filter := infrastructureUseCase.TestLabsFilter{
		Search:   strings.TrimSpace(q.Search),
		Status:   q.Status,
		SortBy:   q.SortBy,
		SortDir:  q.SortDir,
		Page:     q.Page,
		PageSize: q.PageSize,
	}
	if q.Status != "" && q.Status != "active" && q.Status != "expired" {
		return filter, errors.New("status must be active or expired")
	}
	if !validSort(q.SortBy, q.SortDir, "exercise", "author", "createdAt", "expiresAt") {
		return filter, errInvalidSort
	}
	for raw, target := range map[string]*uuid.NullUUID{q.Author: &filter.AuthorID, q.Exercise: &filter.ExerciseID} {
		if raw == "" {
			continue
		}
		id, parseErr := uuid.FromString(raw)
		if parseErr != nil {
			return filter, errors.New("author and exercise must be ids")
		}
		*target = uuid.NullUUID{UUID: id, Valid: true}
	}
	return filter, nil
}

// bulkRecreateStands godoc
//
//	@Summary	Recreate many team stands
//	@Description	1 to 50 stands; each gets the checks of the single recreation; one result and one audit entry per stand in request order, a refusal never stops the others. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Accept		json
//	@Produce	json
//	@Param		body	body	bulkRecreateRequest	true	"stands"
//	@Success	200	{object}	response.Response{data=bulkResponse}
//	@Router		/infrastructure/stands/bulk/recreate [post]
func (h *Handler) bulkRecreateStands(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req bulkRecreateRequest
	if bindErr := ctx.ShouldBindJSON(&req); bindErr != nil {
		response.AbortWithBadRequest(ctx, bindErr)
		return
	}
	if len(req.Items) == 0 || len(req.Items) > maxBulkStands {
		response.AbortWithBadRequest(ctx, errors.New("provide 1 to 50 stands"))
		return
	}
	audit.SetTarget(ctx, "bulk:recreate")
	out := bulkResponse{Results: make([]bulkResult, 0, len(req.Items))}
	seen := make(map[bulkStandItem]struct{}, len(req.Items))
	for _, item := range req.Items {
		if _, dup := seen[item]; dup {
			continue
		}
		seen[item] = struct{}{}
		result := bulkResult{EventID: item.EventID, TeamID: item.TeamID, Ok: true}
		status := http.StatusOK
		if _, recreateErr := h.useCase.RecreateTeamStand(
			ctx.Request.Context(),
			item.EventID,
			item.TeamID,
			claims.UserID,
		); recreateErr != nil {
			result.Ok = false
			result.Code, result.Error, status = describeBulkError(recreateErr)
		}
		out.Results = append(out.Results, result)
		h.auditBulkItem(
			ctx,
			claims.UserID,
			"event:"+item.EventID.String()+" team:"+item.TeamID.String(),
			status,
		)
	}
	response.AbortWithData(ctx, out)
}

// bulkTerminateTestLabs godoc
//
//	@Summary	End many catalog test labs
//	@Description	1 to 100 labs; each gets the checks of the single termination; one result and one audit entry per lab in request order. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Accept		json
//	@Produce	json
//	@Param		body	body	bulkTerminateRequest	true	"test labs"
//	@Success	200	{object}	response.Response{data=bulkResponse}
//	@Router		/infrastructure/test-labs/bulk/terminate [post]
func (h *Handler) bulkTerminateTestLabs(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req bulkTerminateRequest
	if bindErr := ctx.ShouldBindJSON(&req); bindErr != nil {
		response.AbortWithBadRequest(ctx, bindErr)
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBulkTestLabs {
		response.AbortWithBadRequest(ctx, errors.New("provide 1 to 100 test laboratories"))
		return
	}
	results, terminateErr := h.useCase.BulkTerminateTestLabs(ctx.Request.Context(), req.IDs)
	if terminateErr != nil {
		response.AbortWithError(ctx, terminateErr)
		return
	}
	audit.SetTarget(ctx, "bulk:terminate")
	out := bulkResponse{Results: make([]bulkResult, 0, len(results))}
	for _, r := range results {
		result := bulkResult{ID: r.ID, Ok: r.Err == nil}
		status := http.StatusOK
		if r.Err != nil {
			result.Code, result.Error, status = describeBulkError(r.Err)
		}
		out.Results = append(out.Results, result)
		h.auditBulkItem(ctx, claims.UserID, "test-lab:"+r.ID.String(), status)
	}
	response.AbortWithData(ctx, out)
}

// auditBulkItem writes the audit entry of one item of a bulk request.
func (h *Handler) auditBulkItem(ctx *gin.Context, actor uuid.UUID, target string, status int) {
	_ = h.useCase.RecordAdminAction(ctx.Request.Context(), adminAuditUseCase.Entry{
		ActorID:        actor,
		Permission:     string(rbac.PermInfrastructureWrite),
		Method:         ctx.Request.Method,
		Route:          ctx.FullPath(),
		ResponseStatus: status,
		Target:         target,
	})
}

// describeBulkError turns an error into the catalog code, the English text and the HTTP status the single action
// would have answered.
func describeBulkError(e error) (string, string, int) {
	var coded err.Error
	if errors.As(e, &coded) {
		inner := coded.UnwrapNotInternalError()
		if !inner.StatusCode().IsInternal() {
			return strconv.Itoa(
					inner.StatusCode().FullCode(),
				), inner.StatusCode().
					Message(),
				inner.StatusCode().
					HTTPCode()
		}
	}
	return "", "Internal server error", http.StatusInternalServerError
}
