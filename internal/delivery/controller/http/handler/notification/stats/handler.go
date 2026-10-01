package stats

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/pagination"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		ListDispatches(
			ctx context.Context,
			f dispatchModel.ListDispatchesFilter,
		) ([]dispatchModel.DispatchDetail, int64, error)
		GetDispatch(ctx context.Context, id uuid.UUID) (*dispatchModel.DispatchDetail, error)
		GetStats(ctx context.Context, since time.Time) (*dispatchModel.Stats, error)
	}

	dispatchResponse struct {
		ID               uuid.UUID  `json:"ID"`
		NotificationType string     `json:"NotificationType"`
		RecipientUserID  uuid.UUID  `json:"RecipientUserID"`
		RecipientEmail   string     `json:"RecipientEmail"`
		RecipientName    string     `json:"RecipientName"`
		ScopeEventID     *uuid.UUID `json:"ScopeEventID"`
		EventName        string     `json:"EventName"`
		Status           string     `json:"Status"`
		CreatedAt        time.Time  `json:"CreatedAt"`
		UpdatedAt        time.Time  `json:"UpdatedAt"`
	}
	targetResponse struct {
		Channel   string `json:"Channel"`
		Status    string `json:"Status"`
		Error     string `json:"Error"`
		Attempts  int32  `json:"Attempts"`
		Transport string `json:"Transport"`
		// ErrorKind and ErrorCode classify a failed email (dispatchModel.MailError*,
		// the SMTP reply code); empty when the text is not a transport error.
		ErrorKind string `json:"ErrorKind"`
		ErrorCode string `json:"ErrorCode"`
		// Recipient is the address of an email target; an in-app target has none
		// of its own, so it shows the dispatch's recipient user. RecipientName is
		// that user's name when the target belongs to them.
		Recipient     string `json:"Recipient"`
		RecipientName string `json:"RecipientName"`
		FallbackError string `json:"FallbackError"`
		// FallbackErrorKind and FallbackErrorCode classify FallbackError the same way.
		FallbackErrorKind string    `json:"FallbackErrorKind"`
		FallbackErrorCode string    `json:"FallbackErrorCode"`
		UpdatedAt         time.Time `json:"UpdatedAt"`
	}
	dispatchDetailResponse struct {
		dispatchResponse
		Targets []targetResponse `json:"Targets"`
	}
	keyCountResponse struct {
		Key   string `json:"Key"`
		Count int64  `json:"Count"`
	}
	channelStatusResponse struct {
		Channel string `json:"Channel"`
		Status  string `json:"Status"`
		Count   int64  `json:"Count"`
	}
	statsResponse struct {
		Since     time.Time               `json:"Since"`
		Total     int64                   `json:"Total"`
		ByStatus  []keyCountResponse      `json:"ByStatus"`
		ByType    []keyCountResponse      `json:"ByType"`
		ByChannel []channelStatusResponse `json:"ByChannel"`
	}
)

func NewStatsAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	router.GET("stats", h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead), h.getStats)
	dispatches := router.Group(
		"dispatches",
		h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
	)
	dispatches.GET("", h.listDispatches)
	dispatches.GET(":id", h.getDispatch)
}

func toDispatchResponse(d dispatchModel.DispatchInfo) dispatchResponse {
	return dispatchResponse{
		ID: d.ID, NotificationType: d.NotificationType, RecipientUserID: d.RecipientUserID,
		RecipientEmail: d.RecipientEmail, RecipientName: d.RecipientName, ScopeEventID: d.ScopeEventID, EventName: d.EventName,
		Status: d.Status, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func toDetailResponse(d dispatchModel.DispatchDetail) dispatchDetailResponse {
	out := dispatchDetailResponse{
		dispatchResponse: toDispatchResponse(d.DispatchInfo),
		Targets:          make([]targetResponse, 0, len(d.Targets)),
	}
	for _, t := range d.Targets {
		out.Targets = append(out.Targets, toTargetResponse(d.DispatchInfo, t))
	}
	return out
}

// toTargetResponse maps one target. The in-app copy stores no address, so the
// dispatch's recipient user stands in (the join also covers older rows); an
// email target shows the user's name when it went to their own address.
func toTargetResponse(d dispatchModel.DispatchInfo, t dispatchModel.DispatchTarget) targetResponse {
	out := targetResponse{
		Channel: t.Channel, Status: t.Status, Error: t.Error, Attempts: t.Attempts,
		Transport: t.Transport, Recipient: t.Recipient, FallbackError: t.FallbackError, UpdatedAt: t.UpdatedAt,
	}
	switch {
	case t.Channel == "in_app" && t.Recipient == "":
		out.Recipient, out.RecipientName = d.RecipientEmail, d.RecipientName
	case t.Recipient != "" && strings.EqualFold(t.Recipient, d.RecipientEmail):
		out.RecipientName = d.RecipientName
	}
	if t.Channel == "email" {
		out.ErrorKind, out.ErrorCode = dispatchModel.ClassifyMailError(t.Error)
		out.FallbackErrorKind, out.FallbackErrorCode = dispatchModel.ClassifyMailError(t.FallbackError)
	}
	return out
}

// getStats godoc
// @Summary  Notification dispatch statistics
// @Tags     notification-stats
// @Produce  json
// @Param    days  query  int  false  "window in days (default 30)"
// @Success  200  {object}  response.Response{data=statsResponse}
// @Router   /notifications/stats [get]
func (h *Handler) getStats(ctx *gin.Context) {
	days := parseInt(ctx.Query("days"), 30)
	since := time.Now().AddDate(0, 0, -days)
	s, err := h.useCase.GetStats(ctx.Request.Context(), since)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := statsResponse{Since: s.Since, Total: s.Total}
	for _, k := range s.ByStatus {
		out.ByStatus = append(out.ByStatus, keyCountResponse{Key: k.Key, Count: k.Count})
	}
	for _, k := range s.ByType {
		out.ByType = append(out.ByType, keyCountResponse{Key: k.Key, Count: k.Count})
	}
	for _, c := range s.ByChannel {
		out.ByChannel = append(
			out.ByChannel,
			channelStatusResponse{Channel: c.Channel, Status: c.Status, Count: c.Count},
		)
	}
	if out.ByStatus == nil {
		out.ByStatus = []keyCountResponse{}
	}
	if out.ByType == nil {
		out.ByType = []keyCountResponse{}
	}
	if out.ByChannel == nil {
		out.ByChannel = []channelStatusResponse{}
	}
	response.AbortWithData(ctx, out)
}

// listDispatches godoc
// @Summary  List notification dispatches (log)
// @Tags     notification-stats
// @Produce  json
// @Param    type    query  string  false  "filter by notification type"
// @Param    status  query  string  false  "filter by dispatch status"
// @Param    user    query  string  false  "filter by recipient user id (must be a valid UUID)"
// @Param    event      query  string  false  "filter by Event id"
// @Param    channel    query  string  false  "target channel (email, in_app)"
// @Param    result     query  string  false  "target status (done, error)"
// @Param    transport  query  string  false  "email route (event, platform, env)"
// @Param    limit   query  int     false  "page size (default 50)"
// @Param    cursor  query  string  false  "last dispatch ID from the previous cursor page"
// @Success  200  {object}  response.Response{data=pagination.CursorPage[dispatchDetailResponse]}
// @Router   /notifications/dispatches [get]
func (h *Handler) listDispatches(ctx *gin.Context) {
	f, limit, ok := ParseJournalFilter(ctx)
	if !ok {
		return
	}
	rows, total, err := h.useCase.ListDispatches(ctx.Request.Context(), f)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	WriteJournalPage(ctx, rows, total, limit)
}

// ParseJournalFilter reads the journal query (type, status, user, event,
// channel, result, transport, limit, cursor). It aborts with 400 and returns
// ok=false on invalid input. The Event journal reuses it and then forces the
// event filter.
func ParseJournalFilter(ctx *gin.Context) (dispatchModel.ListDispatchesFilter, int, bool) {
	for _, key := range []string{"user", "event", "cursor"} {
		if raw := ctx.Query(key); raw != "" {
			if _, err := uuid.FromString(raw); err != nil {
				response.AbortWithBadRequest(ctx, fmt.Errorf("invalid %s: %w", key, err))
				return dispatchModel.ListDispatchesFilter{}, 0, false
			}
		}
	}
	limit := parseInt(ctx.Query("limit"), 50)
	if limit < 1 || limit > pagination.MaxPageSize {
		response.AbortWithBadRequest(ctx, fmt.Errorf("limit must be between 1 and %d", pagination.MaxPageSize))
		return dispatchModel.ListDispatchesFilter{}, 0, false
	}
	return dispatchModel.ListDispatchesFilter{
		Type:      ctx.Query("type"),
		Status:    ctx.Query("status"),
		User:      ctx.Query("user"),
		Event:     ctx.Query("event"),
		Channel:   ctx.Query("channel"),
		Result:    ctx.Query("result"),
		Transport: ctx.Query("transport"),
		Cursor:    ctx.Query("cursor"),
		Limit:     int32(limit + 1), // one extra row reveals whether a next page exists
	}, limit, true
}

// WriteJournalPage writes a cursor page of journal rows fetched with
// limit+1 rows.
func WriteJournalPage(ctx *gin.Context, rows []dispatchModel.DispatchDetail, total int64, limit int) {
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]dispatchDetailResponse, 0, len(rows))
	for _, r := range rows {
		items = append(items, toDetailResponse(r))
	}
	next := uuid.Nil
	if len(rows) > 0 {
		next = rows[len(rows)-1].ID
	}
	response.AbortWithData(ctx, pagination.NewCursorPage(items, hasMore, next, total))
}

// getDispatch godoc
// @Summary  Get a dispatch with its targets
// @Tags     notification-stats
// @Produce  json
// @Param    id  path  string  true  "dispatch ID"
// @Success  200  {object}  response.Response{data=dispatchDetailResponse}
// @Failure  404  {object}  response.Response
// @Router   /notifications/dispatches/{id} [get]
func (h *Handler) getDispatch(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	d, err := h.useCase.GetDispatch(ctx.Request.Context(), id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toDetailResponse(*d))
}

// parseInt parses a non-negative decimal with a default.
func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil || v < 0 {
		return def
	}
	return v
}
