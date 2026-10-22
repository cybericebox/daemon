package eventself

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/limits"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// Screen links (live screen for projector PCs that are not signed in): the
// token in ?token= opens the published layout of its screen (an event has up
// to 10) and the staff live board, read-only. Nothing else is reachable with it.
func (h *Handler) initLiveScreen(router *gin.RouterGroup, resolveTenant gin.HandlerFunc) {
	known := func(ctx context.Context, token string, now time.Time) bool {
		return h.useCase.IsActiveLiveScreenToken(ctx, token, now)
	}
	screen := router.Group("events/self/live-screen", h.prot.RequirePermission(rbac.PermEventContentRead), liveScreenNoStore, newLiveScreenLimiter(known).middleware, h.liveScreenGate, resolveTenant)
	screen.GET("", h.liveScreen)
	screen.GET("version", h.liveScreenVersion)
	screen.GET("results", h.liveScreenAccess, h.resultsSnapshot)
	screen.GET("results/live", h.liveScreenAccess, h.liveResults)
}

// LiveScreenTokenHeader carries the screen link token: the event site's
// server and the screen's own JSON calls send it here, so it stays out of
// URLs. The query parameter remains for the results stream (EventSource
// cannot send headers); the request log never writes query values.
const LiveScreenTokenHeader = "X-Live-Screen-Token"

// liveScreenToken is the token of a screen request: the header, else ?token=.
func liveScreenToken(ctx *gin.Context) string {
	if token := ctx.GetHeader(LiveScreenTokenHeader); token != "" {
		return token
	}
	return ctx.Request.URL.Query().Get("token")
}

// liveScreenNoStore: an answer opened with a token is for that screen only;
// nothing on the way may keep it, and the page never sends its address on.
func liveScreenNoStore(ctx *gin.Context) {
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Next()
}

// liveScreenGate lets a working screen link of the event open it before
// publication (no session on a projector PC): the tenant middleware admits a
// non-public event only when the link resolves to that very event; a token
// of another event, an expired or a revoked one stays a bare 404.
func (h *Handler) liveScreenGate(ctx *gin.Context) {
	token := liveScreenToken(ctx)
	gate := func(c context.Context, eventID uuid.UUID) bool {
		return token != "" && h.useCase.ResolveLiveScreenToken(c, eventID, token) == nil
	}
	ctx.Request = ctx.Request.WithContext(middleware.ContextWithPrivateEventGate(ctx.Request.Context(), gate))
	ctx.Next()
}

type liveScreenResponse struct {
	Event publicEventInfoResponse `json:"Event"`
	// Screen is the screen the link belongs to.
	Screen liveScreenIdentityResponse `json:"Screen"`
	Layout json.RawMessage            `json:"Layout"`
	// Stages in the event order; the screen timer counts to the current
	// stage end or the next stage start. Empty for an event without stages.
	Stages []liveScreenStageResponse `json:"Stages"`
}

type liveScreenIdentityResponse struct {
	ID   uuid.UUID `json:"ID"`
	Name string    `json:"Name"`
}

type liveScreenVersionResponse struct {
	Version  int64     `json:"Version"`
	ScreenID uuid.UUID `json:"ScreenID"`
}

type liveScreenStageResponse struct {
	Name     string    `json:"Name"`
	OpensAt  time.Time `json:"OpensAt"`
	ClosesAt time.Time `json:"ClosesAt"`
}

// liveScreen godoc
// @Summary Open the live screen with a screen link
// @Tags events-self
// @Produce json
// @Param X-Live-Screen-Token header string false "screen link token (or ?token=)"
// @Param token query string false "screen link token, for clients that cannot send the header"
// @Success 200 {object} response.Response{data=liveScreenResponse}
// @Router /events/self/live-screen [get]
func (h *Handler) liveScreen(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetLiveScreen(ctx, tenant.EventID, liveScreenToken(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	layout, err := json.Marshal(v.Layout)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	stages := make([]liveScreenStageResponse, 0, len(v.Stages))
	for _, stage := range v.Stages {
		stages = append(stages, liveScreenStageResponse{Name: stage.Name, OpensAt: stage.OpensAt, ClosesAt: stage.ClosesAt})
	}
	response.AbortWithData(ctx, liveScreenResponse{Event: toPublicEventInfoResponse(v.Event), Screen: liveScreenIdentityResponse{ID: v.ScreenID, Name: v.ScreenName}, Layout: layout, Stages: stages})
}

// liveScreenVersion godoc
// @Summary Read the published layout version of a screen link's screen
// @Description A light check for an open screen: reload the layout (GET /events/self/live-screen) only when the version changes.
// @Tags events-self
// @Produce json
// @Param X-Live-Screen-Token header string false "screen link token (or ?token=)"
// @Param token query string false "screen link token, for clients that cannot send the header"
// @Success 200 {object} response.Response{data=liveScreenVersionResponse}
// @Router /events/self/live-screen/version [get]
func (h *Handler) liveScreenVersion(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetLiveScreenVersion(ctx, tenant.EventID, liveScreenToken(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, liveScreenVersionResponse{Version: v.Version, ScreenID: v.ScreenID})
}

// liveScreenAccess turns a valid screen link into the read-only staff view
// of this event's live board (view=live) and hands over to the regular
// results handlers; an invalid link stops here.
func (h *Handler) liveScreenAccess(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	// Read the raw query: ctx.Query would cache it before it is rewritten.
	query := ctx.Request.URL.Query()
	token := liveScreenToken(ctx)
	if err := h.useCase.ResolveLiveScreenToken(ctx, tenant.EventID, token); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Params = append(ctx.Params, gin.Param{Key: "id", Value: tenant.EventID.String()})
	query.Del("token")
	query.Set("view", "live")
	ctx.Request.URL.RawQuery = query.Encode()
	// No session and no role: only resultsAccess below, for this event,
	// turns the mark into the screen view.
	screenCtx := context.WithValue(ctx.Request.Context(), liveScreenEventKey{}, tenant.EventID)
	// The token stays with the request: a long stream re-checks it, so a revoked link stops the stream.
	ctx.Request = ctx.Request.WithContext(context.WithValue(screenCtx, liveScreenTokenKey{}, token))
	ctx.Next()
}

type (
	liveScreenEventKey struct{}
	liveScreenTokenKey struct{}
)

// resultsAccess is the reader of a results request: a live screen checked
// for this very event, or the optional session.
func resultsAccess(ctx *gin.Context, eventID uuid.UUID) eventUseCase.ResultsAccess {
	if screenEvent, ok := ctx.Request.Context().Value(liveScreenEventKey{}).(uuid.UUID); ok && screenEvent == eventID {
		return eventUseCase.LiveScreenResultsAccess
	}
	access := eventUseCase.ResultsAccess{Role: rbac.RolePublic}
	if claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); found {
		access.UserID, access.Role = &claims.UserID, claims.Role
	}
	return access
}

// liveScreenLimiter bounds requests per screen link (never per client
// address): a screen polls a few times a minute, far below the limit. Only
// a working link has its own budget; every unknown or malformed token
// shares one, so made-up tokens can neither grow the counts nor take a real
// screen's budget. Built when the routes are, from the configured limit.
func newLiveScreenLimiter(known func(ctx context.Context, token string, now time.Time) bool) *windowLimiter {
	limiter := newWindowLimiter(limits.Get().LiveScreenPerMinute, time.Minute)
	limiter.known = known
	return limiter
}

type windowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	start  time.Time
	counts map[string]int
	known  func(ctx context.Context, token string, now time.Time) bool
}

func newWindowLimiter(limit int, window time.Duration) *windowLimiter {
	return &windowLimiter{limit: limit, window: window, counts: map[string]int{}}
}

// allow counts one request of key in the current fixed window. The counts
// start over with each window, so a link that stopped working leaves them
// within a minute.
func (l *windowLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.start) >= l.window {
		l.start, l.counts = now, map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= l.limit, l.window - now.Sub(l.start)
}

// unknownScreenBucket is the shared budget of every token that is not a working link.
const unknownScreenBucket = ""

func (l *windowLimiter) middleware(ctx *gin.Context) {
	now := time.Now()
	token := liveScreenToken(ctx)
	key := unknownScreenBucket
	if l.known != nil && l.known(ctx.Request.Context(), token, now) {
		key = "link:" + token
	}
	if ok, wait := l.allow(key, now); !ok {
		response.AbortWithTooManyRequests(ctx, wait)
		return
	}
	ctx.Next()
}
