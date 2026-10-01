package eventself

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// The screen link check of the fake use case: only this token opens the
// screen of fakeScreenEvent.
var fakeScreenEvent = uuid.Must(uuid.NewV7())

const fakeScreenToken = "valid-screen-token"

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
	return eventUseCase.LiveScreenView{Event: eventUseCase.EventInfoView{EventID: eventID, Name: "Screen"}, Layout: eventContentModel.DefaultLiveLayout()}, nil
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

func TestLiveScreenReturnsTheEventAndLayout(t *testing.T) {
	router := screenRouter(NewEventSelfAPIHandler(fakeUseCase{}, nil), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/live-screen?token="+fakeScreenToken, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Screen"`) || !strings.Contains(w.Body.String(), `"widgets"`) {
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
	if !limiter.allow("a", now) || !limiter.allow("a", now) || limiter.allow("a", now) {
		t.Fatal("the third request in a window must be refused")
	}
	if !limiter.allow("b", now) {
		t.Fatal("limits are per client")
	}
	if !limiter.allow("a", now.Add(time.Minute)) {
		t.Fatal("a new window starts over")
	}
}
