package event

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/err"
)

// valueTrue is the value of a boolean query filter that is switched on.
const valueTrue = "true"

type (
	bulkArchiveRequest struct {
		EventIDs []uuid.UUID `json:"EventIDs"`
	}

	bulkEventResultResponse struct {
		EventID uuid.UUID `json:"EventID"`
		Ok      bool      `json:"Ok"`
		// Code is the catalog code of the refusal (empty when Ok); Error its English text.
		Code  string `json:"Code,omitempty"`
		Error string `json:"Error,omitempty"`
	}

	bulkArchiveResponse struct {
		Results []bulkEventResultResponse `json:"Results"`
	}
)

// parseEventListFilters reads the table filters of the events list: startFrom / startTo (RFC 3339, to is exclusive),
// organizer (a user id) and infrastructure (true | false).
func parseEventListFilters(ctx *gin.Context, f *eventUseCase.ListEventsFilter) error {
	for key, target := range map[string]**time.Time{"startFrom": &f.StartFrom, "startTo": &f.StartTo} {
		if raw := ctx.Query(key); raw != "" {
			t, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				return errors.New(key + " must be an RFC 3339 time")
			}
			*target = &t
		}
	}
	if f.StartFrom != nil && f.StartTo != nil && !f.StartFrom.Before(*f.StartTo) {
		return errors.New("startFrom must be before startTo")
	}
	if raw := ctx.Query("organizer"); raw != "" {
		id, parseErr := uuid.FromString(raw)
		if parseErr != nil {
			return errors.New("organizer must be a user id")
		}
		f.OrganizerID = id
	}
	switch infra := ctx.Query("infrastructure"); infra {
	case "", valueTrue, "false":
		f.Infrastructure = infra
	default:
		return errors.New("infrastructure must be true or false")
	}
	switch needs := ctx.Query("needsReservation"); needs {
	case "", valueTrue:
		f.NeedsReservation = needs
	default:
		return errors.New("needsReservation must be true")
	}
	return nil
}

// bulkArchive godoc
// @Summary  Archive many events
// @Description 1 to 200 events; one result per event in request order; an event that fails never stops the others; one audit entry per event.
// @Tags     events
// @Accept   json
// @Produce  json
// @Param    body  body      bulkArchiveRequest  true  "events"
// @Success  200  {object}  response.Response{data=bulkArchiveResponse}
// @Router   /events/bulk/archive [post]
func (h *Handler) bulkArchive(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req bulkArchiveRequest
	if bindErr := ctx.ShouldBindJSON(&req); bindErr != nil {
		response.AbortWithBadRequest(ctx, bindErr)
		return
	}
	if len(req.EventIDs) == 0 || len(req.EventIDs) > eventUseCase.MaxBulkEvents {
		response.AbortWithBadRequest(ctx, errors.New("provide 1 to 200 events"))
		return
	}
	audit.SetTarget(ctx, "bulk:archive")
	out := bulkArchiveResponse{Results: make([]bulkEventResultResponse, 0, len(req.EventIDs))}
	seen := make(map[uuid.UUID]struct{}, len(req.EventIDs))
	for _, id := range req.EventIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		item := bulkEventResultResponse{EventID: id, Ok: true}
		status := http.StatusOK
		if _, archiveErr := h.useCase.ArchiveEvent(
			ctx.Request.Context(),
			id,
			claims.UserID,
		); archiveErr != nil {
			item.Ok = false
			item.Code, item.Error, status = describeBulkError(archiveErr)
		}
		out.Results = append(out.Results, item)
		_ = h.useCase.RecordAdminAction(ctx.Request.Context(), adminAuditUseCase.Entry{
			ActorID:        claims.UserID,
			Permission:     string(rbac.PermEventsWrite),
			Method:         ctx.Request.Method,
			Route:          ctx.FullPath(),
			ResponseStatus: status,
			Target:         "event:" + id.String(),
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
