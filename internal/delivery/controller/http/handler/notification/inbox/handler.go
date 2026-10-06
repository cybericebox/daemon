package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IUseCase interface {
		ListInbox(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category, before *inboxModel.Cursor) (inboxModel.Page, error)
		PollInbox(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, since *inboxModel.Cursor) (inboxModel.PollResult, error)
		MarkRead(ctx context.Context, userID, id uuid.UUID) error
		MarkAllRead(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category) error
		ResolveRequest(ctx context.Context, userID, id uuid.UUID) error
		ListBanners(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) ([]inboxModel.InAppNotification, error)
		DismissBanner(ctx context.Context, userID, id uuid.UUID) error
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

type notificationResponse struct {
	ID            uuid.UUID       `json:"ID"`
	Title         string          `json:"Title"`
	Body          string          `json:"Body"`
	Link          string          `json:"Link"`
	Icon          string          `json:"Icon"`
	Tone          string          `json:"Tone"`
	AccentColor   string          `json:"AccentColor"`
	Surface       string          `json:"Surface"`
	AutoDismissMs *int32          `json:"AutoDismissMs"`
	Actions       json.RawMessage `json:"Actions" swaggertype:"object"`
	Dismissible   bool            `json:"Dismissible"`
	ReadAt        *time.Time      `json:"ReadAt"`
	CreatedAt     time.Time       `json:"CreatedAt"`
	EventID       *uuid.UUID      `json:"EventID"`
	EventName     *string         `json:"EventName"`
	EventTag      *string         `json:"EventTag"`
	// Type is the notification type (e.g. event.lab.failed).
	Type string `json:"Type"`
	// Category is the inbox tab, computed by the server.
	Category string `json:"Category" enums:"requests,personal,activity"`
	// ActionRequired is true only for requests.
	ActionRequired bool `json:"ActionRequired"`
	// ResolvedAt and Resolution are set once a request was decided (by anyone).
	ResolvedAt *time.Time `json:"ResolvedAt"`
	Resolution *string    `json:"Resolution" enums:"approved,rejected,fixed,resolved,expired,withdrawn"`
	// ResolvedBy is the person who resolved it (null for system resolutions).
	ResolvedBy *resolvedByResponse `json:"ResolvedBy"`
}

type resolvedByResponse struct {
	ID   uuid.UUID `json:"ID"`
	Name string    `json:"Name"`
}

// countsResponse: Requests = open requests; Personal and Activity = unread;
// All = their sum (the bell badge).
type countsResponse struct {
	All      int64 `json:"All"`
	Requests int64 `json:"Requests"`
	Personal int64 `json:"Personal"`
	Activity int64 `json:"Activity"`
}

type pollResponse struct {
	Cursor      *inboxModel.Cursor     `json:"Cursor"`
	NewInbox    []notificationResponse `json:"NewInbox"`
	UnreadCount int64                  `json:"UnreadCount"`
	Counts      countsResponse         `json:"Counts"`
	// OtherEventsCount: unread items and open requests of other Events;
	// non-zero only for ?event=<id>.
	OtherEventsCount int64 `json:"OtherEventsCount"`
}

type listResponse struct {
	Items      []notificationResponse `json:"Items"`
	NextCursor *inboxModel.Cursor     `json:"NextCursor"`
}

func toResponse(n inboxModel.InAppNotification) notificationResponse {
	out := notificationResponse{
		ID:    n.ID,
		Title: n.Title,
		Body:  n.Body,
		Link:  n.Link,
		Icon:  n.Icon, Tone: n.Tone, AccentColor: n.AccentColor, Surface: n.Surface,
		AutoDismissMs: n.AutoDismissMs, Actions: n.Actions, Dismissible: n.Dismissible,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
		Type:      n.Type, Category: string(n.Category), ActionRequired: n.ActionRequired,
		ResolvedAt: n.ResolvedAt,
	}
	if n.ResolvedAt != nil {
		out.Resolution = &n.Resolution
	}
	if n.ResolvedBy != nil {
		out.ResolvedBy = &resolvedByResponse{ID: *n.ResolvedBy, Name: n.ResolvedByName}
	}
	if n.EventID != nil {
		out.EventID, out.EventName, out.EventTag = n.EventID, &n.EventName, &n.EventTag
	}
	return out
}

// eventScopeNone is the ?event= value for platform-only inboxes.
const eventScopeNone = "none"

// eventScope reads the optional ?event= inbox scope: absent lists every item,
// an Event id lists that Event's items plus items without an Event (M5), and
// "none" lists only items without an Event (every non-Event site).
func eventScope(ctx *gin.Context) (*uuid.UUID, bool) {
	raw := ctx.Query("event")
	if raw == "" {
		return nil, true
	}
	if raw == eventScopeNone {
		return new(inboxModel.PlatformScope), true
	}
	id, err := uuid.FromString(raw)
	if err != nil {
		response.AbortWithBadRequest(ctx, fmt.Errorf("invalid event id"))
		return nil, false
	}
	return &id, true
}

// inboxCategory reads the optional ?category= tab filter; absent = every tab.
func inboxCategory(ctx *gin.Context) (inboxModel.Category, bool) {
	raw := ctx.Query("category")
	if raw == "" {
		return "", true
	}
	category, ok := inboxModel.ParseCategory(raw)
	if !ok {
		response.AbortWithBadRequest(ctx, fmt.Errorf("invalid inbox category"))
		return "", false
	}
	return category, true
}

func NewInboxAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	router.GET("banners", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.listBanners)
	router.PATCH("banners/:id/dismiss", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.dismissBanner)
	inboxAPI := router.Group("inbox")
	{
		inboxAPI.GET("", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.listInbox)
		inboxAPI.GET("poll", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.pollInbox)
		inboxAPI.PATCH(":id/read", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.markRead)
		inboxAPI.POST(":id/resolve", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.resolveRequest)
		inboxAPI.PATCH(
			"read-all",
			h.prot.RequirePermission(rbac.PermNotificationsSelf),
			h.markAllRead,
		)
	}
}

// pollInbox godoc
// @Summary  Poll the inbox for new entries, the unread count and tab counts
// @Description  Returns a baseline cursor on the first call and only new inbox entries on subsequent calls. Read state is reflected in UnreadCount. Counts has the tab badges in the scope (Requests = open requests, Personal/Activity = unread, All = sum); OtherEventsCount counts unread items and open requests of other Events when event is an Event id.
// @Tags     notification-inbox
// @Produce  json
// @Param    since_id  query  string  false  "cursor id (with since_at)"
// @Param    since_at  query  string  false  "cursor time, RFC3339Nano (with since_id)"
// @Param    event  query  string  false  "Event id: that Event's items plus items without an Event; none: only items without an Event"
// @Success  200  {object}  response.Response{data=pollResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/inbox/poll [get]
func (h *Handler) pollInbox(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var since *inboxModel.Cursor
	idText, atText := ctx.Query("since_id"), ctx.Query("since_at")
	if idText != "" || atText != "" {
		if idText == "" || atText == "" {
			response.AbortWithBadRequest(ctx, fmt.Errorf("since_id and since_at must be provided together"))
			return
		}
		id, idErr := uuid.FromString(idText)
		at, atErr := time.Parse(time.RFC3339Nano, atText)
		if idErr != nil || atErr != nil {
			response.AbortWithBadRequest(ctx, fmt.Errorf("invalid inbox cursor"))
			return
		}
		since = &inboxModel.Cursor{ID: id, CreatedAt: at}
	}
	scope, ok := eventScope(ctx)
	if !ok {
		return
	}
	result, err := h.useCase.PollInbox(ctx, claims.UserID, scope, since)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]notificationResponse, 0, len(result.NewInbox))
	for _, item := range result.NewInbox {
		items = append(items, toResponse(item))
	}
	response.AbortWithData(ctx, pollResponse{
		Cursor: result.Cursor, NewInbox: items, UnreadCount: result.UnreadCount,
		Counts: countsResponse{
			All: result.Counts.All, Requests: result.Counts.Requests,
			Personal: result.Counts.Personal, Activity: result.Counts.Activity,
		},
		OtherEventsCount: result.OtherEventsCount,
	})
}

func (h *Handler) listBanners(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	scope, ok := eventScope(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListBanners(ctx, claims.UserID, scope)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]notificationResponse, 0, len(items))
	for _, it := range items {
		out = append(out, toResponse(it))
	}
	response.AbortWithData(ctx, out)
}

func (h *Handler) dismissBanner(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.DismissBanner(ctx, claims.UserID, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// listInbox godoc
// @Summary  List in-app notifications for the current user
// @Tags     notification-inbox
// @Produce  json
// @Param    event  query  string  false  "Event id: that Event's items plus items without an Event; none: only items without an Event"
// @Param    category  query  string  false  "Inbox tab; absent = all"  Enums(requests, personal, activity)
// @Param    before_id  query  string  false  "page cursor id (with before_at)"
// @Param    before_at  query  string  false  "page cursor time, RFC3339Nano (with before_id)"
// @Success  200  {object}  response.Response{data=listResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/inbox [get]
func (h *Handler) listInbox(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var before *inboxModel.Cursor
	idText, atText := ctx.Query("before_id"), ctx.Query("before_at")
	if idText != "" || atText != "" {
		if idText == "" || atText == "" {
			response.AbortWithBadRequest(ctx, fmt.Errorf("before_id and before_at must be provided together"))
			return
		}
		id, idErr := uuid.FromString(idText)
		at, atErr := time.Parse(time.RFC3339Nano, atText)
		if idErr != nil || atErr != nil {
			response.AbortWithBadRequest(ctx, fmt.Errorf("invalid inbox page cursor"))
			return
		}
		before = &inboxModel.Cursor{ID: id, CreatedAt: at}
	}
	scope, ok := eventScope(ctx)
	if !ok {
		return
	}
	category, ok := inboxCategory(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.ListInbox(ctx, claims.UserID, scope, category, before)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]notificationResponse, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, toResponse(it))
	}
	response.AbortWithData(ctx, listResponse{Items: out, NextCursor: page.NextCursor})
}

// markRead godoc
// @Summary  Mark a single notification as read
// @Tags     notification-inbox
// @Produce  json
// @Param    id   path      string  true  "notification ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /notifications/inbox/{id}/read [patch]
func (h *Handler) markRead(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.MarkRead(ctx, userID, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// markAllRead godoc
// @Summary  Mark all notifications as read for the current user
// @Description  Open requests become read but stay open until resolved.
// @Tags     notification-inbox
// @Produce  json
// @Param    event  query  string  false  "Event id: that Event's items plus items without an Event; none: only items without an Event"
// @Param    category  query  string  false  "Only this inbox tab; absent = all"  Enums(requests, personal, activity)
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /notifications/inbox/read-all [patch]
func (h *Handler) markAllRead(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	scope, ok := eventScope(ctx)
	if !ok {
		return
	}
	category, ok := inboxCategory(ctx)
	if !ok {
		return
	}
	if err := h.useCase.MarkAllRead(ctx, userID, scope, category); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// resolveRequest godoc
// @Summary  Resolve an inbox request by hand
// @Description  Closes an open request for every recipient (Resolution "resolved", ResolvedBy = caller). Only a recipient of the item may call it, and only for requests without a domain decision of their own (event.lab.failed); applications and proposals close when approved or rejected.
// @Tags     notification-inbox
// @Produce  json
// @Param    id   path      string  true  "notification ID (the caller's copy)"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /notifications/inbox/{id}/resolve [post]
func (h *Handler) resolveRequest(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.ResolveRequest(ctx, claims.UserID, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
