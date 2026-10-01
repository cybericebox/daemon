package messaging

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	"github.com/cybericebox/daemon/internal/model/rbac"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
	"github.com/cybericebox/daemon/pkg/pagination"
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
// @Success  200  {object}  response.Response{data=pagination.CursorPage[broadcastResponse]}
// @Router   /notifications/broadcasts [get]
// @Router   /events/{id}/manage/broadcasts [get]
func (h *Handler) listBroadcasts(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
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
// @Param    limit        query  int     false  "page size (default 50)"
// @Param    offset       query  int     false  "offset"
// @Success  200  {object}  response.Response{data=[]deliveryResponse}
// @Router   /notifications/broadcasts/{broadcastID}/deliveries [get]
// @Router   /events/{id}/manage/broadcasts/{broadcastID}/deliveries [get]
func (h *Handler) listDeliveries(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := h.broadcastRef(ctx, scope)
		if !ok {
			return
		}
		limit, err := parseLimit(ctx.Query("limit"), 50)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		var offset int
		if raw := ctx.Query("offset"); raw != "" {
			if _, err = fmt.Sscanf(raw, "%d", &offset); err != nil || offset < 0 {
				response.AbortWithBadRequest(ctx, fmt.Errorf("invalid offset"))
				return
			}
		}
		if _, err = h.useCase.GetBroadcast(ctx.Request.Context(), id, eventID); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		items, err := h.useCase.ListBroadcastDeliveries(ctx.Request.Context(), id, int32(limit), int32(offset))
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		out := make([]deliveryResponse, 0, len(items))
		for _, d := range items {
			out = append(out, deliveryResponse(d))
		}
		response.AbortWithData(ctx, out)
	}
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
