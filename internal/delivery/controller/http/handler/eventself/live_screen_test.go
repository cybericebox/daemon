package eventself

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/sse"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// The screen link check of the fake use case: only this token opens the
// screen of fakeScreenEvent.
var fakeScreenEvent = uuid.Must(uuid.NewV7())

const fakeScreenToken = "valid-screen-token"

func (fakeUseCase) IsActiveLiveScreenToken(_ context.Context, token string, _ time.Time) bool {
	return token == fakeScreenToken
}

func (fakeUseCase) ResolveLiveScreenToken(_ context.Context, eventID uuid.UUID, token string) error {
	if eventID != fakeScreenEvent || token != fakeScreenToken {
		return eventContentModel.ErrLiveScreenTokenInvalid.Err()
	}
	return nil
}

func (f fakeUseCase) GetLiveScreen(ctx context.Context, eventID uuid.UUID, token string) (eventUseCase.LiveScreenView, error) {
	if err := f.ResolveLiveScreenToken(ctx, eventID, token); err != nil {
		return eventUseCase.LiveScreenView{}, err
	}
	opens := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	return eventUseCase.LiveScreenView{Event: eventUseCase.EventInfoView{EventID: eventID, Name: "Screen"}, Layout: eventContentModel.DefaultLiveLayout(),
		Stages: []eventUseCase.LiveScreenStageView{{Name: "Етап 1", OpensAt: opens, ClosesAt: opens.Add(2 * time.Hour)}}}, nil
}

func screenRouter(h *Handler, next gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// The error middleware of the app renders AbortWithError; here any
	// error is a 403, which is what a screen link error maps to.
	router.Use(func(ctx *gin.Context) {
		ctx.Next()
		if failure, found := ctx.Get(response.ErrorCtxKey); found && errors.Is(failure.(error), eventContentModel.ErrLiveScreenTokenInvalid.Err()) {
			ctx.Status(http.StatusForbidden)
		}
	})
	tenant := func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(middleware.ContextWithEventTenant(ctx.Request.Context(), middleware.EventTenant{EventID: fakeScreenEvent}))
		ctx.Next()
	}
	router.GET("/live-screen", tenant, h.liveScreen)
	router.GET("/live-screen/results", tenant, h.liveScreenAccess, next)
	return router
}

func TestLiveScreenAccessOpensTheStaffBoardOnlyWithAValidLink(t *testing.T) {
	var seen *gin.Context
	router := screenRouter(NewEventSelfAPIHandler(fakeUseCase{}, nil), func(ctx *gin.Context) { seen = ctx; ctx.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/live-screen/results?token=wrong", nil))
	if w.Code != http.StatusForbidden || seen != nil {
		t.Fatalf("invalid link: status %d, results reached = %v", w.Code, seen != nil)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/live-screen/results?token="+fakeScreenToken+"&view=page", nil))
	if w.Code != http.StatusNoContent || seen == nil {
		t.Fatalf("valid link: status %d", w.Code)
	}
	if seen.Param("id") != fakeScreenEvent.String() || seen.Query("view") != "live" || seen.Query("token") != "" {
		t.Fatalf("handed over id=%q view=%q token=%q", seen.Param("id"), seen.Query("view"), seen.Query("token"))
	}
	// The link gives no session or role to anything else…
	if _, found := rbac.CurrentUserSessionFromContext(seen.Request.Context()); found {
		t.Fatal("a screen link must not create a session")
	}
	// …only the screen view of this event's results.
	if access := resultsAccess(seen, fakeScreenEvent); !access.Screen || access.UserID != nil || access.Role != rbac.RolePublic {
		t.Fatalf("access = %+v", access)
	}
	if access := resultsAccess(seen, uuid.Must(uuid.NewV7())); access.Screen {
		t.Fatal("the screen mark must not open another event's results")
	}
}

// sessionProtection gates like the real protection: a permission that needs
// authentication needs a session in the request context.
type sessionProtection struct{}

func (sessionProtection) RequirePermission(permission rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
		if permission.RequireAuthentication() && !found {
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if found && !claims.Role.HasPermission(permission) {
			ctx.AbortWithStatus(http.StatusForbidden)
			return
		}
		ctx.Next()
	}
}

// A valid screen link means nothing outside the live-screen routes: the
// token is not read there and no session exists.
func TestScreenLinkIsRejectedOnEveryOtherEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var accesses []eventUseCase.ResultsAccess
	uc := fakeUseCase{results: func(_ context.Context, _ uuid.UUID, access eventUseCase.ResultsAccess) (eventUseCase.ResultsSnapshotView, error) {
		accesses = append(accesses, access)
		return eventUseCase.ResultsSnapshotView{}, nil
	}}
	router := gin.New()
	tenant := func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(middleware.ContextWithEventTenant(ctx.Request.Context(), middleware.EventTenant{EventID: fakeScreenEvent}))
		ctx.Next()
	}
	NewEventSelfAPIHandler(uc, sessionProtection{}).Init(router.Group("/api"), tenant)
	event := fakeScreenEvent.String()
	for _, path := range []string{
		"/api/events/self/info", "/api/events/self/participant-info", "/api/events/self/participant-form",
		"/api/events/" + event + "/teams/mine", "/api/events/" + event + "/teams/challenges/mine",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?token="+fakeScreenToken, nil))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("%s with a screen link answered %d", path, w.Code)
		}
	}
	// The public results endpoints read the optional session, never the link.
	for _, path := range []string{"/api/events/" + event + "/results?view=live&token=" + fakeScreenToken, "/api/events/" + event + "/results?token=" + fakeScreenToken} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/self/live-screen/results?token="+fakeScreenToken, nil))
	if len(accesses) != 3 || accesses[0].Screen || accesses[1].Screen || !accesses[2].Screen {
		t.Fatalf("results accesses = %+v", accesses)
	}
}

func TestLiveScreenReturnsTheEventLayoutAndStages(t *testing.T) {
	router := screenRouter(NewEventSelfAPIHandler(fakeUseCase{}, nil), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/live-screen?token="+fakeScreenToken, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Screen"`) || !strings.Contains(w.Body.String(), `"widgets"`) ||
		!strings.Contains(w.Body.String(), `"Stages":[{"Name":"Етап 1","OpensAt":"2026-10-19T10:00:00Z","ClosesAt":"2026-10-19T12:00:00Z"}]`) {
		t.Fatalf("live screen = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/live-screen", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("no token = %d", w.Code)
	}
}

func TestWindowLimiter(t *testing.T) {
	limiter := newWindowLimiter(2, time.Minute)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	allow := func(key string, at time.Time) bool {
		ok, _ := limiter.allow(key, at)
		return ok
	}
	if !allow("a", now) || !allow("a", now) || allow("a", now) {
		t.Fatal("the third request in a window must be refused")
	}
	if !allow("b", now) {
		t.Fatal("limits are per key")
	}
	if !allow("a", now.Add(time.Minute)) {
		t.Fatal("a new window starts over")
	}
}

// limiterRouter serves /screen behind a limiter of `limit` per minute that
// knows the given working links.
func limiterRouter(limit int, links ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	working := map[string]bool{}
	for _, link := range links {
		working[link] = true
	}
	limiter := newWindowLimiter(limit, time.Minute)
	limiter.known = func(_ context.Context, token string, _ time.Time) bool { return working[token] }
	r := gin.New()
	r.GET("/screen", limiter.middleware, func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func screenGet(r *gin.Engine, token string) int {
	req := httptest.NewRequest(http.MethodGet, "/screen?token="+token, nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// The limiter counts per screen link, never per client address.
func TestLiveScreenLimiterKeysOnTheLinkNotTheAddress(t *testing.T) {
	a, b := screenToken(1), screenToken(2)
	r := limiterRouter(1, a, b)
	if screenGet(r, a) != http.StatusOK || screenGet(r, b) != http.StatusOK {
		t.Fatal("two links behind one address have separate budgets")
	}
	if screenGet(r, a) != http.StatusTooManyRequests {
		t.Fatal("a second request of the same link is over the limit of 1")
	}
	// Tokens that are no working link share one budget.
	if screenGet(r, "junk-1") != http.StatusOK || screenGet(r, screenToken(3)) != http.StatusTooManyRequests {
		t.Fatal("unknown tokens must share one budget")
	}
}

// screenToken is a well-formed (32-byte) token.
func screenToken(fill byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

// Made-up tokens neither grow the counts nor push a working link into the
// shared budget: after 2000 random tokens (and the shared budget spent) the
// link keeps its own.
func TestRandomTokensCannotTakeAWorkingLinksBudget(t *testing.T) {
	link := screenToken(7)
	r := limiterRouter(5, link)
	random := make([]byte, 32)
	for i := 0; i < 2000; i++ {
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		screenGet(r, base64.RawURLEncoding.EncodeToString(random))
	}
	if screenGet(r, screenToken(9)) != http.StatusTooManyRequests {
		t.Fatal("the shared budget of unknown tokens is spent")
	}
	for i := 0; i < 5; i++ {
		if code := screenGet(r, link); code != http.StatusOK {
			t.Fatalf("request %d of the working link = %d", i+1, code)
		}
	}
}

func TestWindowLimiterKeepsOnlyTheSharedAndLinkKeys(t *testing.T) {
	link := screenToken(7)
	limiter := newWindowLimiter(1, time.Minute)
	limiter.known = func(_ context.Context, token string, _ time.Time) bool { return token == link }
	r := gin.New()
	r.GET("/screen", limiter.middleware, func(c *gin.Context) { c.Status(http.StatusOK) })
	for i := 0; i < 100; i++ {
		screenGet(r, fmt.Sprintf("token-%d", i))
	}
	screenGet(r, link)
	if len(limiter.counts) != 2 {
		t.Fatalf("limiter keeps %d keys, want the shared one and the link", len(limiter.counts))
	}
}

// A new window or a spent shared budget never stops a working link: the
// shared budget is only the junk's, and the link's own count starts over.
func TestWindowResetAndJunkFloodKeepWorkingLinksServed(t *testing.T) {
	link := screenToken(7)
	limiter := newWindowLimiter(3, time.Minute)
	limiter.known = func(_ context.Context, token string, _ time.Time) bool { return token == link }
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for minute := 0; minute < 3; minute++ {
		at := start.Add(time.Duration(minute) * time.Minute)
		// At the start of each window: 1100 made-up tokens and junk first.
		for i := 0; i < 1100; i++ {
			limiter.allow(unknownScreenBucket, at)
		}
		if ok, _ := limiter.allow("link:"+link, at.Add(time.Second)); !ok {
			t.Fatalf("minute %d: the working link was refused", minute)
		}
	}
}

// unpublishedResolver resolves every tag to one non-public event.
type unpublishedResolver struct{ eventID uuid.UUID }

func (r unpublishedResolver) ResolveEventByTag(context.Context, string, time.Time) (middleware.EventTenant, error) {
	return middleware.EventTenant{EventID: r.eventID, Tag: "khctf", Public: false}, nil
}

func (unpublishedResolver) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("no session expected")
}

// A screen link opens its own unpublished event with no session; a link of
// another event, a bad, expired or revoked one, or none gets the same bare 404
// as an unknown host.
func TestScreenLinkOpensItsOwnUnpublishedEventOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEventSelfAPIHandler(fakeUseCase{}, nil)
	serve := func(eventID uuid.UUID, origin, token string) *httptest.ResponseRecorder {
		r := gin.New()
		resolve := middleware.ResolveEventTenant(unpublishedResolver{eventID: eventID}, config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"})
		r.GET("/api/events/self/live-screen", h.liveScreenGate, resolve, h.liveScreen)
		req := httptest.NewRequest(http.MethodGet, "/api/events/self/live-screen?token="+token, nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := serve(fakeScreenEvent, "https://khctf.example.test", fakeScreenToken); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Screen"`) {
		t.Fatalf("own link on the unpublished event = %d %s", w.Code, w.Body.String())
	}
	unknownHost := serve(fakeScreenEvent, "https://elsewhere.invalid", fakeScreenToken)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"a valid link on another event's site": serve(uuid.Must(uuid.NewV7()), "https://other.example.test", fakeScreenToken),
		"an expired, revoked or unknown link":  serve(fakeScreenEvent, "https://khctf.example.test", "revoked-token"),
		"no link":                              serve(fakeScreenEvent, "https://khctf.example.test", ""),
	} {
		if w.Code != http.StatusNotFound || w.Body.String() != unknownHost.Body.String() {
			t.Fatalf("%s = %d %q, want the unknown host answer %d %q", name, w.Code, w.Body.String(), unknownHost.Code, unknownHost.Body.String())
		}
	}
}

// The event site's server sends the token as a header: it opens the screen
// like ?token= does, and the token never needs to be in a URL.
func TestLiveScreenTokenComesInTheHeader(t *testing.T) {
	router := screenRouter(NewEventSelfAPIHandler(fakeUseCase{}, nil), func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/live-screen", nil)
	req.Header.Set(LiveScreenTokenHeader, fakeScreenToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Screen"`) {
		t.Fatalf("header token = %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/live-screen/results", nil)
	req.Header.Set(LiveScreenTokenHeader, fakeScreenToken)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("header token on results = %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/live-screen", nil)
	req.Header.Set(LiveScreenTokenHeader, "wrong")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a wrong header token = %d", w.Code)
	}
}

// Answers opened with a screen token are never stored, and the page sends no referrer.
func TestLiveScreenAnswersAreNeverStored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/screen", liveScreenNoStore, func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/stream", liveScreenNoStore, func(c *gin.Context) { sse.WriteHeaders(c); c.Status(http.StatusOK) })
	for _, path := range []string{"/screen", "/stream"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("%s Referrer-Policy = %q", path, got)
		}
	}
}
