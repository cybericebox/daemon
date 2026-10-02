package eventself

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/errjournal"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// Screen links (live screen for projector PCs that are not signed in): the
// token in ?token= opens this event's published layout and the staff live
// board, read-only. Nothing else is reachable with it.
func (h *Handler) initLiveScreen(router *gin.RouterGroup, resolveTenant gin.HandlerFunc) {
	screen := router.Group("events/self/live-screen", h.prot.RequirePermission(rbac.PermEventContentRead), liveScreenLimiter.middleware, resolveTenant)
	screen.GET("", h.liveScreen)
	screen.GET("results", h.liveScreenAccess, h.resultsSnapshot)
	screen.GET("results/live", h.liveScreenAccess, h.liveResults)
}

type liveScreenResponse struct {
	Event  publicEventInfoResponse `json:"Event"`
	Layout json.RawMessage         `json:"Layout"`
}

// liveScreen godoc
// @Summary Open the live screen with a screen link
// @Tags events-self
// @Produce json
// @Param token query string true "screen link token"
// @Success 200 {object} response.Response{data=liveScreenResponse}
// @Router /events/self/live-screen [get]
func (h *Handler) liveScreen(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetLiveScreen(ctx, tenant.EventID, ctx.Query("token"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	layout, err := json.Marshal(v.Layout)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, liveScreenResponse{Event: toPublicEventInfoResponse(v.Event), Layout: layout})
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
	token := query.Get("token")
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

// liveScreenLimiter bounds screen-link requests per client address: a
// screen polls a few times a minute, far below the limit.
var liveScreenLimiter = newWindowLimiter(120, time.Minute)

type windowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	start  time.Time
	counts map[string]int
}

func newWindowLimiter(limit int, window time.Duration) *windowLimiter {
	return &windowLimiter{limit: limit, window: window, counts: map[string]int{}}
}

// allow counts one request of key in the current fixed window.
func (l *windowLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.start) >= l.window {
		l.start, l.counts = now, map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= l.limit
}

func (l *windowLimiter) middleware(ctx *gin.Context) {
	if !l.allow(ctx.ClientIP(), time.Now()) {
		errjournal.SetLimiter(ctx, "live-screen-window")
		response.AbortWithTooManyRequests(ctx)
		return
	}
	ctx.Next()
}
