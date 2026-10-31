package errorJournal

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	errorJournalModel "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	"github.com/cybericebox/daemon/pkg/err"
)

// maxBulkGroups is the most groups one bulk request takes.
const maxBulkGroups = 200

type (
	bulkGroupsRequest struct {
		GroupIDs []uuid.UUID `json:"GroupIDs"`
	}

	bulkGroupResultResponse struct {
		GroupID uuid.UUID `json:"GroupID"`
		Ok      bool      `json:"Ok"`
		// Code is the catalog code of the refusal (empty when Ok); Error its English text.
		Code  string `json:"Code,omitempty"`
		Error string `json:"Error,omitempty"`
	}

	bulkGroupsResponse struct {
		Results []bulkGroupResultResponse `json:"Results"`
	}
)

// bulkSetStatus answers the bulk routes resolve, ignore and reopen: the same status for many groups.
//
// @Summary  Resolve, ignore or reopen many error groups
// @Description 1 to 200 groups; one result per group in request order; a group that fails never stops the others; one audit entry per group. The routes are /admin/errors/bulk/resolve, /admin/errors/bulk/ignore and /admin/errors/bulk/reopen.
// @Tags     error-journal
// @Accept   json
// @Produce  json
// @Param    body  body      bulkGroupsRequest  true  "groups"
// @Success  200   {object}  response.Response{data=bulkGroupsResponse}
// @Router   /admin/errors/bulk/resolve [post]
// @Router   /admin/errors/bulk/ignore [post]
// @Router   /admin/errors/bulk/reopen [post]
func (h *Handler) bulkSetStatus(status errorJournalModel.Status) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := rbac.CurrentUserSessionFromContext(c.Request.Context())
		if !ok {
			response.AbortWithUnauthenticated(c)
			return
		}
		var req bulkGroupsRequest
		if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
			response.AbortWithBadRequest(c, bindErr)
			return
		}
		if len(req.GroupIDs) == 0 || len(req.GroupIDs) > maxBulkGroups {
			response.AbortWithBadRequest(c, errors.New("provide 1 to 200 groups"))
			return
		}
		audit.SetTarget(c, "bulk:"+string(status))
		out := bulkGroupsResponse{Results: make([]bulkGroupResultResponse, 0, len(req.GroupIDs))}
		seen := make(map[uuid.UUID]struct{}, len(req.GroupIDs))
		for _, id := range req.GroupIDs {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			item := bulkGroupResultResponse{GroupID: id, Ok: true}
			code := http.StatusOK
			if _, setErr := h.useCase.SetErrorGroupStatus(c.Request.Context(), id, status); setErr != nil {
				item.Ok = false
				item.Code, item.Error, code = describeBulkError(setErr)
			}
			out.Results = append(out.Results, item)
			_ = h.useCase.RecordAdminAction(c.Request.Context(), adminAuditUseCase.Entry{
				ActorID: claims.UserID, Permission: string(rbac.PermPlatformErrorsWrite), Method: c.Request.Method,
				Route: c.FullPath(), ResponseStatus: code, Target: "errorGroup:" + id.String(),
			})
		}
		response.AbortWithData(c, out)
	}
}

// describeBulkError turns an error into the catalog code, the English text and the HTTP status the single action
// would have answered.
func describeBulkError(e error) (string, string, int) {
	var coded err.Error
	if errors.As(e, &coded) {
		inner := coded.UnwrapNotInternalError()
		if !inner.StatusCode().IsInternal() {
			return strconv.Itoa(inner.StatusCode().FullCode()), inner.StatusCode().Message(), inner.StatusCode().HTTPCode()
		}
	}
	return "", "Internal server error", http.StatusInternalServerError
}
