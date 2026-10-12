package errjournal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type fakeSink struct {
	mu       sync.Mutex
	events   []errorJournal.Event
	notFound []string
}

func (s *fakeSink) Report(e errorJournal.Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}
func (s *fakeSink) CountNotFound(route string) {
	s.mu.Lock()
	s.notFound = append(s.notFound, route)
	s.mu.Unlock()
}

func newRouter(sink Sink) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	r.Use(Middleware(sink))
	r.Use(response.WithErrorHandler)
	return r
}

func do(r *gin.Engine, method, path string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestFiveHundredIsRecordedWithRouteTemplateStatusAndRequestID(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.GET("/api/things/:id", func(c *gin.Context) { response.AbortWithError(c, errors.New("db exploded for anna@example.org")) })

	w := do(r, http.MethodGet, "/api/things/0198c1f2-7b3a-7c11-9d2e-3f4a5b6c7d8e")
	require.Equal(t, 500, w.Code)
	require.Len(t, sink.events, 1)
	e := sink.events[0]
	assert.Equal(t, errorJournal.KindHTTP5xx, e.Kind)
	assert.Equal(t, "/api/things/:id", e.Route)
	assert.Equal(t, "/api/things/:id", e.Source, "the template, never the raw path")
	assert.Equal(t, 500, e.HTTPStatus)
	assert.Equal(t, w.Header().Get(HeaderRequestID), e.RequestID)
	assert.NotEmpty(t, e.RequestID)
}

func TestRequestCancellationIsNotAPlatformFault(t *testing.T) {
	for _, tc := range []struct {
		name      string
		request   error
		failure   error
		ginError  bool
		extra     error
		wantEvent bool
	}{
		{name: "request canceled", request: context.Canceled, failure: context.Canceled},
		{name: "wrapped request canceled", request: context.Canceled, failure: fmt.Errorf("fetch banners: %w", context.Canceled)},
		{name: "domain-wrapped request canceled", request: context.Canceled, failure: model.ErrPlatform.WithError(context.Canceled).Err()},
		{name: "gin request canceled", request: context.Canceled, failure: context.Canceled, ginError: true},
		{name: "gin cancellation and server fault", request: context.Canceled, failure: context.Canceled, ginError: true, extra: errors.New("database failed"), wantEvent: true},
		{name: "dependency canceled with active request", failure: context.Canceled, wantEvent: true},
		{name: "server fault after cancellation", request: context.Canceled, failure: errors.New("database failed"), wantEvent: true},
		{name: "joined server fault and cancellation", request: context.Canceled, failure: errors.Join(context.Canceled, errors.New("database failed")), wantEvent: true},
		{name: "request deadline exceeded", request: context.DeadlineExceeded, failure: context.DeadlineExceeded, wantEvent: true},
		{name: "dependency deadline after cancellation", request: context.Canceled, failure: context.DeadlineExceeded, wantEvent: true},
		{name: "status without error after cancellation", request: context.Canceled, wantEvent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeSink{}
			r := newRouter(sink)
			r.GET("/api/banners", func(c *gin.Context) {
				if tc.ginError {
					_ = c.Error(tc.failure)
					if tc.extra != nil {
						_ = c.Error(tc.extra)
					}
					c.AbortWithStatus(http.StatusInternalServerError)
				} else if tc.failure != nil {
					response.AbortWithError(c, tc.failure)
				} else {
					c.AbortWithStatus(http.StatusInternalServerError)
				}
			})
			req := httptest.NewRequest(http.MethodGet, "/api/banners", nil)
			switch tc.request {
			case context.Canceled:
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			case context.DeadlineExceeded:
				ctx, cancel := context.WithDeadline(req.Context(), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
				defer cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusInternalServerError, w.Code, "classification does not change the HTTP response")
			if tc.wantEvent {
				require.Len(t, sink.events, 1)
				require.Equal(t, errorJournal.KindHTTP5xx, sink.events[0].Kind)
			} else {
				require.Empty(t, sink.events, "only a canceled request and its cancellation error are routine")
			}
		})
	}
}

func TestPanicIsRecordedWithStackAndStillAnswered(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.GET("/boom", func(*gin.Context) { panic("kaboom") })

	w := do(r, http.MethodGet, "/boom")
	assert.Equal(t, 500, w.Code, "the outer recovery still answers the client")
	require.Len(t, sink.events, 1)
	assert.Equal(t, errorJournal.KindPanic, sink.events[0].Kind)
	assert.Equal(t, "kaboom", sink.events[0].Message)
	assert.Contains(t, sink.events[0].Stack, "goroutine")
}

func TestForbiddenCarriesRoleUserAndThePermission(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	uid := uuid.Must(uuid.NewV7())
	r.GET("/api/admin/x", func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uid, Role: rbac.RoleAdmin}))
		SetPermission(c, "platform.settings.write")
		response.AbortWithForbidden(c)
	})

	assert.Equal(t, 403, do(r, http.MethodGet, "/api/admin/x").Code)
	require.Len(t, sink.events, 1)
	e := sink.events[0]
	assert.Equal(t, errorJournal.KindHTTP403, e.Kind)
	assert.Equal(t, "platform.settings.write", e.Permission)
	assert.Equal(t, string(rbac.RoleAdmin), e.Role)
	require.NotNil(t, e.UserID)
	assert.Equal(t, uid, *e.UserID)
}

func TestBusinessForbiddenIsNotRecorded(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.POST("/api/events/:id/manage/x", func(c *gin.Context) { response.AbortWithForbidden(c) })

	assert.Equal(t, 403, do(r, http.MethodPost, "/api/events/1/manage/x").Code)
	assert.Empty(t, sink.events)
}

// A rate limit refusing a flood is protection working, not a fault: nothing reaches the journal.
func TestTooManyRequestsIsNotRecorded(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.GET("/limited", func(c *gin.Context) { response.AbortWithTooManyRequests(c, time.Second) })

	assert.Equal(t, 429, do(r, http.MethodGet, "/limited").Code)
	assert.Empty(t, sink.events)
}

func TestNotFoundIsCountedNotRecorded(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.GET("/api/events/:id", func(c *gin.Context) { response.AbortWithNotFound(c) })

	do(r, http.MethodGet, "/api/events/42")         // a handler 404
	do(r, http.MethodGet, "/wp-login.php?x=secret") // no route at all
	assert.Empty(t, sink.events)
	assert.Equal(t, []string{"/api/events/:id", ""}, sink.notFound, "an unmatched path is one counter, without the path")
}

func TestUnauthenticatedAndSuccessAreNotRecorded(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.GET("/ok", func(c *gin.Context) { response.AbortWithSuccess(c) })
	r.GET("/who", func(c *gin.Context) { response.AbortWithUnauthenticated(c) })

	do(r, http.MethodGet, "/ok")
	assert.Equal(t, 401, do(r, http.MethodGet, "/who").Code)
	assert.Empty(t, sink.events)
	assert.Empty(t, sink.notFound)
}

func TestRequestIDIsKeptWhenSaneAndReplacedWhenNot(t *testing.T) {
	r := newRouter(&fakeSink{})
	r.GET("/ok", func(c *gin.Context) { response.AbortWithSuccess(c) })

	assert.Equal(t, "client-req-12345", do(r, http.MethodGet, "/ok", HeaderRequestID, "client-req-12345").Header().Get(HeaderRequestID))
	replaced := do(r, http.MethodGet, "/ok", HeaderRequestID, "bad id with spaces <script>").Header().Get(HeaderRequestID)
	assert.NotContains(t, replaced, "script")
	assert.NotEmpty(t, replaced)
}

func TestNoSinkStillAssignsRequestIDs(t *testing.T) {
	r := newRouter(nil)
	r.GET("/ok", func(c *gin.Context) { response.AbortWithSuccess(c) })
	assert.NotEmpty(t, do(r, http.MethodGet, "/ok").Header().Get(HeaderRequestID))
}

func TestEventsNeverCarryAnAddress(t *testing.T) {
	sink := &fakeSink{}
	r := newRouter(sink)
	r.GET("/e", func(c *gin.Context) { response.AbortWithError(c, errors.New("x")) })
	req := httptest.NewRequest(http.MethodGet, "/e", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	r.ServeHTTP(httptest.NewRecorder(), req)
	require.Len(t, sink.events, 1)
	assert.NotContains(t, sink.events[0].Message+sink.events[0].Source+sink.events[0].RequestID, "203.0.113.9")
	assert.Empty(t, sink.events[0].Details)
}
