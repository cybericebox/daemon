package pulse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
	pulseUseCase "github.com/cybericebox/daemon/internal/useCase/notification/pulse"
)

type fakeUseCase struct {
	banners  string
	sites    []siteModel.Site
	viewers  []pulseUseCase.Viewer
	dismiss  uuid.UUID
	dismissU uuid.UUID
}

func (f *fakeUseCase) Pulse(_ context.Context, site siteModel.Site, v pulseUseCase.Viewer) (pulseUseCase.View, error) {
	f.sites, f.viewers = append(f.sites, site), append(f.viewers, v)
	view := pulseUseCase.View{Banners: f.banners}
	if v.UserID != nil {
		view.Inbox = &pulseUseCase.InboxPulse{Cursor: "2026-10-21T12:00:00Z_0190a0b2-0000-7000-8000-000000000001", Unread: 3}
	}
	return view, nil
}

func (f *fakeUseCase) Banners(_ context.Context, site siteModel.Site, v pulseUseCase.Viewer) (pulseUseCase.BannerList, error) {
	f.sites, f.viewers = append(f.sites, site), append(f.viewers, v)
	return pulseUseCase.BannerList{Version: f.banners, Items: []pulseUseCase.Banner{{ID: "system-results-freeze:x", Kind: "system", Text: "Заморожено", Level: "critical"}}}, nil
}

func (f *fakeUseCase) DismissBanner(_ context.Context, userID, id uuid.UUID) error {
	f.dismissU, f.dismiss = userID, id
	return nil
}

type allowAll struct{}

func (allowAll) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

func newRouter(uc *fakeUseCase) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}
	site := middleware.NewSiteResolver(hosts, func(context.Context, string) (uuid.UUID, error) { return uuid.Nil, eventModel.ErrEventNotFound.Err() }).ResolveSite()
	NewPulseAPIHandler(uc, allowAll{}, site).Init(router.Group("/api/notifications"))
	return router
}

func get(router *gin.Engine, path string, signedIn bool, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if signedIn {
		req = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: uuid.FromStringOrNil("0190a0b2-0000-7000-8000-0000000000aa"), Role: rbac.RoleUser}))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestGuestPulseIsSharedAndCacheable(t *testing.T) {
	router := newRouter(&fakeUseCase{banners: "abc123"})
	w := get(router, "/api/notifications/pulse", false, map[string]string{"Origin": "https://example.test"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=15" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if vary := w.Header().Get("Vary"); !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Cookie") {
		t.Fatalf("Vary = %q", vary)
	}
	tag := w.Header().Get("ETag")
	if tag == "" || !strings.Contains(w.Body.String(), `"Banners":"abc123"`) || !strings.Contains(w.Body.String(), `"Inbox":null`) {
		t.Fatalf("body = %s etag=%q", w.Body.String(), tag)
	}
	if again := get(router, "/api/notifications/pulse", false, map[string]string{"Origin": "https://example.test", "If-None-Match": tag}); again.Code != http.StatusNotModified {
		t.Fatalf("unchanged guest pulse = %d", again.Code)
	}
}

func TestSignedInPulseIsPrivateAndRevalidated(t *testing.T) {
	router := newRouter(&fakeUseCase{banners: "abc123"})
	w := get(router, "/api/notifications/pulse?site=admin", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if !strings.Contains(w.Body.String(), `"Inbox":{"Cursor":"2026-10-21T12:00:00Z_0190a0b2-0000-7000-8000-000000000001","Unread":3}`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	tag := w.Header().Get("ETag")
	if again := get(router, "/api/notifications/pulse?site=admin", true, map[string]string{"If-None-Match": tag}); again.Code != http.StatusNotModified {
		t.Fatalf("unchanged pulse = %d", again.Code)
	}
}

func TestPulseNeedsASite(t *testing.T) {
	router := newRouter(&fakeUseCase{})
	if w := get(router, "/api/notifications/pulse", false, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no site, no origin = %d", w.Code)
	}
	if w := get(router, "/api/notifications/pulse", false, map[string]string{"Referer": "https://example.test/"}); w.Code != http.StatusBadRequest {
		t.Fatalf("the referer must not name the site: %d", w.Code)
	}
	if w := get(router, "/api/notifications/pulse", false, map[string]string{"Origin": "https://evil.test"}); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown origin = %d", w.Code)
	}
}

func TestBannersCarryTheVersionAsETag(t *testing.T) {
	uc := &fakeUseCase{banners: "v42"}
	router := newRouter(uc)
	w := get(router, "/api/notifications/banners?site=main", false, nil)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"v42"` {
		t.Fatalf("guest banners: %d etag=%q", w.Code, w.Header().Get("ETag"))
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, no-cache" {
		t.Fatalf("guest Cache-Control = %q", cc)
	}
	if !strings.Contains(w.Body.String(), `"ID":"system-results-freeze:x","Kind":"system","Title":"","Text":"Заморожено"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if again := get(router, "/api/notifications/banners?site=main", false, map[string]string{"If-None-Match": `"v42"`}); again.Code != http.StatusNotModified {
		t.Fatalf("banners with the same version = %d", again.Code)
	}
	w = get(router, "/api/notifications/banners?site=main", true, nil)
	if cc := w.Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Fatalf("signed-in Cache-Control = %q", cc)
	}
	if uc.viewers[0].UserID != nil || uc.viewers[2].UserID == nil {
		t.Fatalf("viewers = %+v", uc.viewers)
	}
}

func TestDismissBanner(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc)
	id := uuid.Must(uuid.NewV7())
	req := httptest.NewRequest(http.MethodPatch, "/api/notifications/banners/"+id.String()+"/dismiss", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", w.Code)
	}
	req = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: id, Role: rbac.RoleUser}))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || uc.dismiss != id {
		t.Fatalf("dismiss = %d %v", w.Code, uc.dismiss)
	}
}
