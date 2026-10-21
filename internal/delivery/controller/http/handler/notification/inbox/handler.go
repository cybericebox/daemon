package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
		// site resolves the requesting site into the request context.
		site gin.HandlerFunc
	}

	IUseCase interface {
		ListInbox(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category, before *inboxModel.Cursor) (inboxModel.Page, error)
		InboxSince(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, since *inboxModel.Cursor) (inboxModel.SinceResult, error)
		MarkRead(ctx context.Context, userID, id uuid.UUID) error
		MarkAllRead(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category) error
		ResolveRequest(ctx context.Context, userID, id uuid.UUID) error
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
	// Resolvable is true when the recipient may close this open request by hand (the resolve endpoint accepts it).
	// Clients show «Вирішено» from it instead of keeping their own list of types.
	Resolvable bool `json:"Resolvable"`
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

// sinceResponse is the news of the inbox after the cursor the client holds.
type sinceResponse struct {
	// Cursor is the newest item the response covers; the client sends it as since next time. It stays
	// behind the pulse cursor while more than a page of news is waiting, so keep asking until they match.
	Cursor string                 `json:"Cursor"`
	Items  []notificationResponse `json:"Items"`
	// Unread is the number of unread items in the site's scope (the same number the pulse reports).
	Unread int64          `json:"Unread"`
	Counts countsResponse `json:"Counts"`
	// OtherEventsCount: unread items and open requests of other Events; non-zero on an event site only.
	OtherEventsCount int64 `json:"OtherEventsCount"`
}

type listResponse struct {
	Items []notificationResponse `json:"Items"`
	// NextCursor is the before value of the next older page, empty on the last page.
	NextCursor string `json:"NextCursor"`
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
		Resolvable: n.IsOpenRequest() && inboxModel.ManuallyResolvable(n.Type),
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

// siteScope is the inbox scope of the request's site (set by the site middleware): an event site
// lists that event's items plus the platform ones, every other site only the platform ones.
func siteScope(ctx *gin.Context) (*uuid.UUID, bool) {
	site, ok := middleware.SiteFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithBadRequest(ctx, errors.New("unknown site"))
		return nil, false
	}
	return site.InboxScope(), true
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

func NewInboxAPIHandler(useCase IUseCase, prot IProtection, site gin.HandlerFunc) *Handler {
	return &Handler{useCase: useCase, prot: prot, site: site}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	inboxAPI := router.Group("inbox")
	{
		inboxAPI.GET("", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.site, h.listInbox)
		inboxAPI.PATCH(":id/read", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.markRead)
		inboxAPI.POST(":id/resolve", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.resolveRequest)
		inboxAPI.PATCH(
			"read-all",
			h.prot.RequirePermission(rbac.PermNotificationsSelf),
			h.site,
			h.markAllRead,
		)
	}
}

// listInbox godoc
// @Summary  List the inbox of the current user, or only what is new after a cursor
// @Description  The site is the site query parameter (with event for an event site), or the request Origin when it is missing. Without since: one page of the site's inbox, newest first (category filters the tab, before pages to older items). With since=<cursor> (the Cursor of the pulse or of the previous answer; empty = baseline): only the items newer than the cursor, oldest first, with Unread and the tab Counts. Counts has the tab badges (Requests = open requests, Personal/Activity = unread, All = sum). Fetch it when the pulse cursor or unread changed.
// @Tags     notification-inbox
// @Produce  json
// @Param    site  query  string  false  "Site; the request Origin when missing"  Enums(main,id,admin,exercises,event)
// @Param    event  query  string  false  "Event id of an event site"
// @Param    category  query  string  false  "Inbox tab; absent = all (list only)"  Enums(requests, personal, activity)
// @Param    before  query  string  false  "page cursor of the older page (list only)"
// @Param    since  query  string  false  "cursor to read the news after; empty = baseline"
// @Success  200  {object}  response.Response{data=listResponse}  "without since"
// @Success  200  {object}  response.Response{data=sinceResponse}  "with since"
// @Failure  400  {object}  response.Response
// @Router   /notifications/inbox [get]
func (h *Handler) listInbox(ctx *gin.Context) {
	// Personal data: never kept by the browser or any cache in between.
	ctx.Header("Cache-Control", "private, no-store")
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	scope, ok := siteScope(ctx)
	if !ok {
		return
	}
	query := ctx.Request.URL.Query()
	if _, hasSince := query["since"]; hasSince {
		if _, hasBefore := query["before"]; hasBefore || query.Get("category") != "" {
			response.AbortWithBadRequest(ctx, errors.New("since cannot be combined with before or category"))
			return
		}
		h.inboxSince(ctx, claims.UserID, scope, query.Get("since"))
		return
	}
	before, err := inboxModel.ParseCursor(query.Get("before"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
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
	next := ""
	if page.NextCursor != nil {
		next = page.NextCursor.String()
	}
	response.AbortWithData(ctx, listResponse{Items: out, NextCursor: next})
}

func (h *Handler) inboxSince(ctx *gin.Context, userID uuid.UUID, scope *uuid.UUID, raw string) {
	since, err := inboxModel.ParseCursor(raw)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	result, err := h.useCase.InboxSince(ctx, userID, scope, since)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]notificationResponse, 0, len(result.NewInbox))
	for _, item := range result.NewInbox {
		items = append(items, toResponse(item))
	}
	cursor := ""
	if result.Cursor != nil {
		cursor = result.Cursor.String()
	}
	response.AbortWithData(ctx, sinceResponse{
		Cursor: cursor, Items: items, Unread: result.UnreadCount,
		Counts: countsResponse{
			All: result.Counts.All, Requests: result.Counts.Requests,
			Personal: result.Counts.Personal, Activity: result.Counts.Activity,
		},
		OtherEventsCount: result.OtherEventsCount,
	})
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
// @Param    site  query  string  false  "Site; the request Origin when missing"  Enums(main,id,admin,exercises,event)
// @Param    event  query  string  false  "Event id of an event site"
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
	scope, ok := siteScope(ctx)
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
