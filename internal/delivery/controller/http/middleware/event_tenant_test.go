package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// fakeEventResolver is a hand-rolled middleware.EventResolver: records
// whether/with-what it was called and returns a canned tenant or error.
type fakeEventResolver struct {
	called   bool
	gotTag   string
	tenant   middleware.EventTenant
	err      error
	readErr  error
	readUser uuid.UUID
}

func (f *fakeEventResolver) RequireReadEvent(_ context.Context, _, userID uuid.UUID) error {
	f.readUser = userID
	return f.readErr
}

func (f *fakeEventResolver) ResolveEventByTag(_ context.Context, tag string, _ time.Time) (middleware.EventTenant, error) {
	f.called = true
	f.gotTag = tag
	if f.err != nil {
		return middleware.EventTenant{}, f.err
	}
	return f.tenant, nil
}

// newEventTenantRouter builds a tiny engine with the middleware in front of a
// trivial handler that reports whether it was reached and echoes the
// resolved tenant's tag back in a header (mirrors cors_test.go's newRouter).
func newEventTenantRouter(resolver middleware.EventResolver, apex string) (*gin.Engine, *bool) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	reached := new(bool)
	r.Use(middleware.ResolveEventTenant(resolver, config.HostsConfig{
		Main: apex, API: "api." + apex, ID: "id." + apex, Admin: "admin." + apex, Exercises: "exercises." + apex, EventDomain: apex,
	}))
	r.GET("/api/events/self", func(c *gin.Context) {
		*reached = true
		tenant, ok := middleware.EventTenantFromContext(c.Request.Context())
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Header("X-Tenant-Tag", tenant.Tag)
		c.Status(http.StatusOK)
	})
	return r, reached
}

func TestResolveEventTenant_ValidOrigin_ResolvesTenant(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	resolver := &fakeEventResolver{
		tenant: middleware.EventTenant{EventID: eventID, Tag: "spring", Public: true, AvailableFrom: from, ArchiveAt: to},
	}
	r, reached := newEventTenantRouter(resolver, "example.test")

	req := httptest.NewRequest(http.MethodGet, "/api/events/self", nil)
	req.Header.Set("Origin", "https://spring.example.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !*reached {
		t.Fatal("handler must be reached on a resolved tenant")
	}
	if !resolver.called || resolver.gotTag != "spring" {
		t.Fatalf("resolver called=%v withTag=%q, want called with tag %q", resolver.called, resolver.gotTag, "spring")
	}
	if got := w.Header().Get("X-Tenant-Tag"); got != "spring" {
		t.Fatalf("tenant not in context: X-Tenant-Tag = %q, want %q", got, "spring")
	}
}

func TestResolveEventTenant_UnpublishedOnlyManagersCanEnter(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	resolver := &fakeEventResolver{tenant: middleware.EventTenant{EventID: eventID, Tag: "spring", Public: false}}
	r, reached := newEventTenantRouter(resolver, "example.test")
	request := func(withUser bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/events/self", nil)
		req.Header.Set("Origin", "https://spring.example.test")
		if withUser {
			req = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: userID}))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := request(false); w.Code != http.StatusNotFound || *reached {
		t.Fatalf("unpublished tenant leaked without identity: status=%d reached=%v", w.Code, *reached)
	}
	resolver.readErr = eventManagerModel.ErrEventManagementForbidden.Err()
	if w := request(true); w.Code != http.StatusNotFound || *reached {
		t.Fatalf("unpublished tenant leaked to non-manager: status=%d reached=%v", w.Code, *reached)
	}
	resolver.readErr = nil
	if w := request(true); w.Code != http.StatusOK || !*reached || resolver.readUser != userID {
		t.Fatalf("event manager could not access unpublished tenant: status=%d reached=%v user=%v", w.Code, *reached, resolver.readUser)
	}
}

func TestResolveEventTenant_UnknownTag_404(t *testing.T) {
	resolver := &fakeEventResolver{err: eventModel.ErrEventNotFound.Err()}
	r, reached := newEventTenantRouter(resolver, "example.test")

	req := httptest.NewRequest(http.MethodGet, "/api/events/self", nil)
	req.Header.Set("Origin", "https://ghost.example.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown tag", w.Code)
	}
	if *reached {
		t.Fatal("handler must not be reached when the resolver reports not-found")
	}
	if !resolver.called || resolver.gotTag != "ghost" {
		t.Fatalf("resolver called=%v withTag=%q, want called with tag %q", resolver.called, resolver.gotTag, "ghost")
	}
}

func TestResolveEventTenant_ReservedOrEmptySubdomain_404WithoutCallingResolver(t *testing.T) {
	for _, origin := range []string{
		"https://example.test",       // apex itself: empty subdomain label
		"https://admin.example.test", // reserved: admin frontend
		"https://id.example.test",    // reserved: id frontend
		"https://api.example.test",   // reserved: this service's own host
	} {
		resolver := &fakeEventResolver{tenant: middleware.EventTenant{Tag: "should-not-be-used"}}
		r, reached := newEventTenantRouter(resolver, "example.test")

		req := httptest.NewRequest(http.MethodGet, "/api/events/self", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("origin %q: status = %d, want 404", origin, w.Code)
		}
		if *reached {
			t.Fatalf("origin %q: handler must not be reached", origin)
		}
		if resolver.called {
			t.Fatalf("origin %q: resolver must not be called for a reserved/empty subdomain", origin)
		}
	}
}

func TestResolveEventTenant_MissingOrInvalidOrigin_404(t *testing.T) {
	tests := map[string]string{
		"missing":      "",
		"unparseable":  "://bad",
		"no-host":      "not-a-url",
		"off-platform": "https://evil-example.test",
	}
	for name, origin := range tests {
		t.Run(name, func(t *testing.T) {
			resolver := &fakeEventResolver{tenant: middleware.EventTenant{Tag: "should-not-be-used"}}
			r, reached := newEventTenantRouter(resolver, "example.test")

			req := httptest.NewRequest(http.MethodGet, "/api/events/self", nil)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			if *reached {
				t.Fatal("handler must not be reached")
			}
			if resolver.called {
				t.Fatal("resolver must not be called")
			}
		})
	}
}
