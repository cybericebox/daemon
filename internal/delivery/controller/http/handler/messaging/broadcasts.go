package messaging

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
	"github.com/cybericebox/daemon/pkg/pagination"
	"github.com/cybericebox/daemon/pkg/tools"
)

func marshalJSON(v any, fallback string) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage(fallback), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

// countAudience godoc
// @Summary  Count the recipients of a broadcast audience
// @Tags     notification-broadcasts
// @Accept   json
// @Produce  json
// @Param    body  body  countRequest  true  "audience"
// @Success  200  {object}  response.Response{data=countResponse}
// @Router   /notifications/broadcasts/audience-count [post]
// @Router   /events/{id}/manage/broadcasts/audience-count [post]
func (h *Handler) countAudience(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		var req countRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		n, err := h.useCase.CountBroadcastAudience(ctx.Request.Context(), eventID, req.Audience.toModel())
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, countResponse{Count: n})
	}
}

// send godoc
// @Summary  Send a custom broadcast now
// @Tags     notification-broadcasts
// @Accept   json
// @Produce  json
// @Param    body  body  sendRequest  true  "message and audience"
// @Success  200  {object}  response.Response{data=broadcastResponse}
// @Router   /notifications/broadcasts [post]
// @Router   /events/{id}/manage/broadcasts [post]
func (h *Handler) send(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
		if !ok {
			response.AbortWithUnauthenticated(ctx)
			return
		}
		var req sendRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		content, err := req.contentDTO.toModel()
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		b, err := h.useCase.SendBroadcast(ctx.Request.Context(), broadcastUseCase.SendInput{
			ScopeEventID: eventID, ActorID: claims.UserID, Content: content, Audience: req.Audience.toModel(),
		})
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toBroadcastResponse(b))
	}
}

// listBroadcasts godoc
// @Summary  Broadcast history
// @Tags     notification-broadcasts
// @Produce  json
// @Param    limit   query  int     false  "page size (default 50)"
// @Param    cursor  query  string  false  "last broadcast ID from the previous page"
// @Param    page      query  int     false  "page number: switches to the page-number mode (OffsetPage answer)"
// @Param    pageSize  query  int     false  "page size of the page-number mode (default 50)"
// @Param    search    query  string  false  "subject, in-app title or event name (page-number mode)"
// @Param    status    query  string  false  "sending | done | failed | canceled (page-number mode)"
// @Param    sortBy    query  string  false  "subject | status | recipients | created (page-number mode)"
// @Param    sortDir   query  string  false  "asc | desc (page-number mode)"
// @Success  200  {object}  response.Response{data=pagination.CursorPage[broadcastResponse]}
// @Router   /notifications/broadcasts [get]
// @Router   /events/{id}/manage/broadcasts [get]
func (h *Handler) listBroadcasts(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		if ctx.Query("page") != "" || ctx.Query("pageSize") != "" || ctx.Query("sortBy") != "" {
			h.listBroadcastsPage(ctx, eventID)
			return
		}
		limit, err := parseLimit(ctx.Query("limit"), 50)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		cursor := ctx.Query("cursor")
		if cursor != "" {
			if _, err = uuid.FromString(cursor); err != nil {
				response.AbortWithBadRequest(ctx, fmt.Errorf("invalid cursor: %w", err))
				return
			}
		}
		filter := broadcastModel.ListFilter{Scope: "platform", Cursor: cursor, Limit: int32(limit + 1)}
		if eventID != nil {
			filter.Scope = eventID.String()
		}
		items, err := h.useCase.ListBroadcasts(ctx.Request.Context(), filter)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		out := make([]broadcastResponse, 0, len(items))
		for _, b := range items {
			out = append(out, toBroadcastResponse(b))
		}
		next := uuid.Nil
		if len(items) > 0 {
			next = items[len(items)-1].ID
		}
		response.AbortWithData(ctx, pagination.NewCursorPage(out, hasMore, next, int64(len(out))))
	}
}

// getBroadcast godoc
// @Summary  Get a broadcast
// @Tags     notification-broadcasts
// @Produce  json
// @Param    broadcastID  path  string  true  "broadcast ID"
// @Success  200  {object}  response.Response{data=broadcastResponse}
// @Failure  404  {object}  response.Response
// @Router   /notifications/broadcasts/{broadcastID} [get]
// @Router   /events/{id}/manage/broadcasts/{broadcastID} [get]
func (h *Handler) getBroadcast(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := h.broadcastRef(ctx, scope)
		if !ok {
			return
		}
		b, err := h.useCase.GetBroadcast(ctx.Request.Context(), id, eventID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toBroadcastResponse(b))
	}
}

// listDeliveries godoc
// @Summary  Recipients of a broadcast with their outcome, failures first
// @Tags     notification-broadcasts
// @Produce  json
// @Param    broadcastID  path   string  true   "broadcast ID"
// @Param    status       query  string  false  "error | done | waiting"
// @Param    page         query  int     false  "page number (default 1)"
// @Param    pageSize     query  int     false  "page size (default 50)"
// @Success  200  {object}  response.Response{data=pagination.OffsetPage[deliveryResponse]}
// @Router   /notifications/broadcasts/{broadcastID}/deliveries [get]
// @Router   /events/{id}/manage/broadcasts/{broadcastID}/deliveries [get]
func (h *Handler) listDeliveries(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := h.broadcastRef(ctx, scope)
		if !ok {
			return
		}
		var page pagination.OffsetParams
		if err := ctx.ShouldBindQuery(&page); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		if err := page.Validate(); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		if _, err := h.useCase.GetBroadcast(ctx.Request.Context(), id, eventID); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		items, total, err := h.useCase.ListBroadcastDeliveries(
			ctx.Request.Context(),
			id,
			ctx.Query("status"),
			int32(page.PageSize), //nolint:gosec // validated: at most pagination.MaxPageSize
			int32(page.Offset()), //nolint:gosec // page and page size are bounded by Validate
		)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		masked, err := h.deliveriesMasked(ctx, eventID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		out := make([]deliveryResponse, 0, len(items))
		for _, d := range items {
			out = append(out, maskDelivery(deliveryResponse(d), masked))
		}
		response.AbortWithData(ctx, pagination.NewOffsetPage(out, total, page.Page, page.PageSize))
	}
}

// exportDeliveries godoc
// @Summary  Recipients of a broadcast with their outcome as CSV
// @Tags     notification-broadcasts
// @Produce  text/csv
// @Param    broadcastID  path   string  true   "broadcast ID"
// @Param    status       query  string  false  "error | done | waiting"
// @Success  200  {file}  file
// @Router   /notifications/broadcasts/{broadcastID}/deliveries/export [get]
// @Router   /events/{id}/manage/broadcasts/{broadcastID}/deliveries/export [get]
func (h *Handler) exportDeliveries(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := h.broadcastRef(ctx, scope)
		if !ok {
			return
		}
		status := ctx.Query("status")
		if !broadcastModel.ValidDeliveryStatus(status) {
			response.AbortWithBadRequest(ctx, errors.New("invalid status"))
			return
		}
		if _, err := h.useCase.GetBroadcast(ctx.Request.Context(), id, eventID); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		masked, err := h.deliveriesMasked(ctx, eventID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		download.ExtendDeadline(ctx)
		ctx.Header("Content-Type", "text/csv; charset=utf-8")
		ctx.Header(
			"Content-Disposition",
			fmt.Sprintf(`attachment; filename="broadcast-%s-deliveries.csv"`, id),
		)
		ctx.Header("Cache-Control", "private, no-store")
		ctx.Status(http.StatusOK)
		_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
		w := csv.NewWriter(ctx.Writer)
		_ = w.Write(
			[]string{
				"DispatchID",
				"RecipientUserID",
				"RecipientEmail",
				"DispatchStatus",
				"Channel",
				"TargetStatus",
				"Error",
			},
		)
		err = h.useCase.ExportBroadcastDeliveries(
			ctx.Request.Context(),
			id,
			status,
			func(d broadcastModel.Delivery) error {
				row := maskDelivery(deliveryResponse(d), masked)
				return w.Write([]string{
					row.DispatchID.String(),
					row.RecipientUserID.String(),
					tools.CSVText(row.RecipientEmail),
					row.DispatchStatus,
					row.Channel,
					row.TargetStatus,
					tools.CSVText(row.Error),
				})
			},
		)
		w.Flush()
		if err != nil {
			// The download has started: the status line is sent, so the failure can only be logged.
			log.Error().
				Err(err).
				Str("broadcast", id.String()).
				Msg("broadcast deliveries export interrupted")
		}
	}
}

// cancelBroadcast godoc
// @Summary  Stop a broadcast that is still being sent
// @Tags     notification-broadcasts
// @Produce  json
// @Param    broadcastID  path  string  true  "broadcast ID"
// @Success  200  {object}  response.Response{data=broadcastResponse}
// @Failure  404  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /notifications/broadcasts/{broadcastID}/cancel [post]
// @Router   /events/{id}/manage/broadcasts/{broadcastID}/cancel [post]
func (h *Handler) cancelBroadcast(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := h.broadcastRef(ctx, scope)
		if !ok {
			return
		}
		b, err := h.useCase.CancelBroadcast(ctx.Request.Context(), id, eventID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toBroadcastResponse(b))
	}
}

// deliveriesMasked reports whether the viewer sees recipients' addresses masked: an event's read-only
// moderator does (owner decision 2026-10-19).
func (h *Handler) deliveriesMasked(ctx *gin.Context, eventID *uuid.UUID) (bool, error) {
	if eventID == nil {
		return false, nil
	}
	private, err := middleware.SeesEventPersonalData(ctx, *eventID, h.useCase.RequireManageEvent)
	if err != nil {
		return false, err
	}
	return !private, nil
}

func maskDelivery(row deliveryResponse, masked bool) deliveryResponse {
	if masked {
		row.Error = dispatchModel.MaskEmailsIn(row.Error, row.RecipientEmail)
		row.RecipientEmail = dispatchModel.MaskEmail(row.RecipientEmail)
	}
	return row
}

func (h *Handler) broadcastRef(ctx *gin.Context, scope scopeFn) (uuid.UUID, *uuid.UUID, bool) {
	eventID, ok := scope(ctx)
	if !ok {
		return uuid.Nil, nil, false
	}
	id, err := uuid.FromString(ctx.Param("broadcastID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, nil, false
	}
	return id, eventID, true
}

// broadcastSortColumns are the columns of the page-number mode a history can be sorted by.
var broadcastSortColumns = map[string]bool{
	"subject":    true,
	"status":     true,
	"recipients": true,
	"created":    true,
}

// listBroadcastsPage answers the history in the page-number mode: page, pageSize, search, status and a sort, with
// the total.
func (h *Handler) listBroadcastsPage(ctx *gin.Context, eventID *uuid.UUID) {
	var page pagination.OffsetParams
	if err := ctx.ShouldBindQuery(&page); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := page.Validate(); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter := broadcastModel.ListFilter{
		Scope:   "platform",
		Search:  strings.TrimSpace(ctx.Query("search")),
		Status:  ctx.Query("status"),
		SortBy:  ctx.Query("sortBy"),
		SortDir: ctx.Query("sortDir"),
		Limit:   int32(page.PageSize), //nolint:gosec // bounded by Validate
		Offset:  int32(page.Offset()), //nolint:gosec // page and page size are bounded by Validate
	}
	if eventID != nil {
		filter.Scope = eventID.String()
	}
	validStatus := filter.Status == "" || filter.Status == string(broadcastModel.StatusSending) ||
		filter.Status == string(
			broadcastModel.StatusDone,
		) || filter.Status == string(broadcastModel.StatusFailed) ||
		filter.Status == string(broadcastModel.StatusCanceled)
	if !validStatus || filter.SortBy != "" && !broadcastSortColumns[filter.SortBy] ||
		filter.SortDir != "" && filter.SortDir != "asc" && filter.SortDir != "desc" {
		response.AbortWithBadRequest(ctx, errors.New("status, sortBy or sortDir is not valid"))
		return
	}
	items, total, err := h.useCase.ListBroadcastsPage(ctx.Request.Context(), filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]broadcastResponse, 0, len(items))
	for _, b := range items {
		out = append(out, toBroadcastResponse(b))
	}
	response.AbortWithData(ctx, pagination.NewOffsetPage(out, total, page.Page, page.PageSize))
}
