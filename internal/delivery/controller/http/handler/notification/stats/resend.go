package stats

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	statsUseCase "github.com/cybericebox/daemon/internal/useCase/notification/stats"
	"github.com/cybericebox/daemon/pkg/err"
)

type (
	resendRequest struct {
		DispatchIDs []uuid.UUID `json:"DispatchIDs"`
	}

	resendResultResponse struct {
		DispatchID uuid.UUID `json:"DispatchID"`
		Ok         bool      `json:"Ok"`
		// Code is the catalog code of the refusal (empty when Ok); Error its English text.
		Code  string `json:"Code,omitempty"`
		Error string `json:"Error,omitempty"`
	}

	resendResponse struct {
		Results []resendResultResponse `json:"Results"`
	}
)

// resendDispatches godoc
// @Summary  Send the failed deliveries of many dispatches again
// @Description 1 to 200 dispatches; the failed channels of each are queued again from the stored content; one result per dispatch in request order; a dispatch that cannot be resent never stops the others; one audit entry per dispatch.
// @Tags     notification-stats
// @Accept   json
// @Produce  json
// @Param    body  body      resendRequest  true  "dispatches"
// @Success  200  {object}  response.Response{data=resendResponse}
// @Router   /notifications/dispatches/resend [post]
func (h *Handler) resendDispatches(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req resendRequest
	if bindErr := ctx.ShouldBindJSON(&req); bindErr != nil {
		response.AbortWithBadRequest(ctx, bindErr)
		return
	}
	if len(req.DispatchIDs) == 0 || len(req.DispatchIDs) > statsUseCase.MaxBulkResend {
		response.AbortWithBadRequest(ctx, errors.New("provide 1 to 200 dispatches"))
		return
	}
	audit.SetTarget(ctx, "bulk:resend")
	results := h.useCase.ResendDispatches(ctx.Request.Context(), req.DispatchIDs)
	out := resendResponse{Results: make([]resendResultResponse, 0, len(results))}
	for _, r := range results {
		item := resendResultResponse{DispatchID: r.DispatchID, Ok: r.Err == nil}
		status := http.StatusOK
		if r.Err != nil {
			item.Code, item.Error, status = describeBulkError(r.Err)
		}
		out.Results = append(out.Results, item)
		_ = h.useCase.RecordAdminAction(ctx.Request.Context(), adminAuditUseCase.Entry{
			ActorID:        claims.UserID,
			Permission:     string(rbac.PermNotificationsBroadcast),
			Method:         ctx.Request.Method,
			Route:          ctx.FullPath(),
			ResponseStatus: status,
			Target:         "dispatch:" + r.DispatchID.String(),
		})
	}
	response.AbortWithData(ctx, out)
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
