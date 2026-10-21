package messaging

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
)

// permProtection lets a request through only when its X-Perms header lists the
// permission the route requires: it stands in for the RBAC middleware.
type permProtection struct{}

func (permProtection) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if required != rbac.PermSelf && required != rbac.PermBannersView && ctx.GetHeader("X-Perms") != string(required) {
			response.AbortWithForbidden(ctx)
			return
		}
		if ctx.GetHeader("X-Anonymous") == "" {
			userID := uuid.Must(uuid.NewV7())
			ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: userID, Role: rbac.Role(ctx.GetHeader("X-Role"))}))
		}
		ctx.Next()
	}
}

// fakeUseCase records calls; the Event access checks follow the caller's
// header: "owner" manages, "viewer" only reads.
type fakeUseCase struct {
	IUseCase
	sent          []broadcastUseCase.SendInput
	created       []*uuid.UUID
	createdInputs []siteBannerModel.Input
	updatedInputs []siteBannerModel.Input
	counted       []*uuid.UUID
	role          string
	deliveries    []broadcastModel.Delivery
}

func (f *fakeUseCase) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	if f.role != "owner" {
		return eventManagerModel.ErrEventManagementForbidden.Err()
	}
	return nil
}
func (f *fakeUseCase) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeUseCase) GetBroadcast(context.Context, uuid.UUID, *uuid.UUID) (broadcastModel.Broadcast, error) {
	return broadcastModel.Broadcast{}, nil
}
func (f *fakeUseCase) ListBroadcastDeliveries(context.Context, uuid.UUID, int32, int32) ([]broadcastModel.Delivery, error) {
	return f.deliveries, nil
}
func (f *fakeUseCase) CountBroadcastAudience(_ context.Context, scope *uuid.UUID, _ broadcastModel.Audience) (int, error) {
	f.counted = append(f.counted, scope)
	return 7, nil
}
func (f *fakeUseCase) SendBroadcast(_ context.Context, in broadcastUseCase.SendInput) (broadcastModel.Broadcast, error) {
	f.sent = append(f.sent, in)
	return broadcastModel.Broadcast{ID: uuid.Must(uuid.NewV7()), Content: in.Content, Audience: in.Audience, RecipientCount: 7}, nil
}
func (f *fakeUseCase) ListBroadcasts(context.Context, broadcastModel.ListFilter) ([]broadcastModel.Broadcast, error) {
	return nil, nil
}
func (f *fakeUseCase) ListSiteBanners(context.Context, *uuid.UUID) ([]siteBannerModel.Banner, error) {
	return nil, nil
}
func (f *fakeUseCase) CreateSiteBanner(_ context.Context, scope *uuid.UUID, _ uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error) {
	f.created = append(f.created, scope)
	f.createdInputs = append(f.createdInputs, in)
	return siteBannerModel.New(scope, nil, in, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)), nil
}
func (f *fakeUseCase) UpdateSiteBanner(_ context.Context, scope *uuid.UUID, _ uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error) {
	f.updatedInputs = append(f.updatedInputs, in)
	return siteBannerModel.New(scope, nil, in, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)), nil
}
func do(t *testing.T, uc *fakeUseCase, method, path, perms, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewMessagingAPIHandler(uc, permProtection{}).Init(router.Group("/api"))
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if perms != "" {
		req.Header.Set("X-Perms", perms)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

const sendBody = `{"Channels":["email"],"Subject":"Hi","EmailBody":[{"type":"paragraph"}],"Audience":{"Kind":"all"}}`

func TestPlatformBroadcastRoutesNeedTheBroadcastPermission(t *testing.T) {
	uc := &fakeUseCase{}
	if w := do(t, uc, http.MethodPost, "/api/notifications/broadcasts", "", sendBody); w.Code != http.StatusForbidden {
		t.Fatalf("send without a permission = %d", w.Code)
	}
	// Managing templates is not enough to send to everyone.
	if w := do(t, uc, http.MethodPost, "/api/notifications/broadcasts", string(rbac.PermNotificationsTemplatesWrite), sendBody); w.Code != http.StatusForbidden {
		t.Fatalf("send with the templates permission = %d", w.Code)
	}
	if w := do(t, uc, http.MethodPost, "/api/notifications/broadcasts", string(rbac.PermNotificationsBroadcast), sendBody); w.Code != http.StatusOK {
		t.Fatalf("send with the broadcast permission = %d", w.Code)
	}
	if len(uc.sent) != 1 || uc.sent[0].ScopeEventID != nil || uc.sent[0].Content.Subject != "Hi" {
		t.Fatalf("sent = %+v", uc.sent)
	}
	if w := do(t, uc, http.MethodPost, "/api/notifications/broadcasts/audience-count", string(rbac.PermNotificationsBroadcast), `{"Audience":{"Kind":"all"}}`); w.Code != http.StatusOK {
		t.Fatalf("count = %d", w.Code)
	}
}

func TestEventBroadcastSendNeedsManageAccess(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	path := "/api/events/" + event.String() + "/manage/broadcasts"

	viewer := &fakeUseCase{role: "viewer"}
	if w := do(t, viewer, http.MethodPost, path, "", sendBody); w.Code != http.StatusForbidden {
		t.Fatalf("a viewer sent a broadcast: %d %s", w.Code, w.Body.String())
	}
	if len(viewer.sent) != 0 {
		t.Fatal("a viewer's broadcast reached the use case")
	}
	if w := do(t, viewer, http.MethodGet, path, "", ""); w.Code != http.StatusOK {
		t.Fatalf("a viewer reads the history: %d", w.Code)
	}

	owner := &fakeUseCase{role: "owner"}
	if w := do(t, owner, http.MethodPost, path, "", sendBody); w.Code != http.StatusOK {
		t.Fatalf("the owner sends: %d %s", w.Code, w.Body.String())
	}
	if len(owner.sent) != 1 || owner.sent[0].ScopeEventID == nil || *owner.sent[0].ScopeEventID != event {
		t.Fatalf("event scope lost: %+v", owner.sent)
	}
}

func TestBannerRoutesSplitReadAndWritePermissions(t *testing.T) {
	uc := &fakeUseCase{}
	body := `{"Text":"Hi","Level":"info","Audience":"everyone","IsActive":true}`
	if w := do(t, uc, http.MethodGet, "/api/notifications/site-banners", string(rbac.PermNotificationsBannersRead), ""); w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	if w := do(t, uc, http.MethodPost, "/api/notifications/site-banners", string(rbac.PermNotificationsBannersRead), body); w.Code != http.StatusForbidden {
		t.Fatalf("create with the read permission = %d", w.Code)
	}
	if w := do(t, uc, http.MethodPost, "/api/notifications/site-banners", string(rbac.PermNotificationsBannersWrite), body); w.Code != http.StatusOK {
		t.Fatalf("create = %d", w.Code)
	}
	if len(uc.created) != 1 || uc.created[0] != nil {
		t.Fatalf("a platform banner must have no event scope: %+v", uc.created)
	}
}

// The recipients of an event broadcast: the owner, write moderators and platform staff (admin_viewer included) see
// the addresses; a read-only moderator gets them masked, in the error text too. The platform route never masks.
func TestBroadcastDeliveriesMaskTheAddressesForAViewer(t *testing.T) {
	deliveries := []broadcastModel.Delivery{{RecipientEmail: "olena@example.com", Channel: "email", Error: "550 <olena@example.com> unknown"}}
	path := "/api/events/" + uuid.Must(uuid.NewV7()).String() + "/manage/broadcasts/" + uuid.Must(uuid.NewV7()).String() + "/deliveries"
	cases := []struct {
		name, role string
		platform   rbac.Role
		private    bool
	}{
		{"owner or write moderator", "owner", "", true},
		{"read-only moderator", "viewer", "", false},
		{"platform admin", "viewer", rbac.RoleAdmin, true},
		{"platform admin viewer", "viewer", rbac.RoleAdminViewer, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeUseCase{role: c.role, deliveries: deliveries}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(response.WithErrorHandler)
			NewMessagingAPIHandler(uc, permProtection{}).Init(router.Group("/api"))
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-Role", string(c.platform))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if strings.Contains(body, "olena@example.com") != c.private || (!c.private && !strings.Contains(body, "o***@example.com")) {
				t.Fatalf("body = %s", body)
			}
		})
	}
	uc := &fakeUseCase{role: "viewer", deliveries: deliveries}
	w := do(t, uc, http.MethodGet, "/api/notifications/broadcasts/"+uuid.Must(uuid.NewV7()).String()+"/deliveries", string(rbac.PermNotificationsBroadcast), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "olena@example.com") {
		t.Fatalf("platform deliveries: %d %s", w.Code, w.Body.String())
	}
}
