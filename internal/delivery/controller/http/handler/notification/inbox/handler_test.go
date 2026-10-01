package inbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type fakeUseCase struct {
	list         []inboxModel.InAppNotification
	gotUserID    uuid.UUID
	listBefore   *inboxModel.Cursor
	bannerUserID uuid.UUID
	pollSince    *inboxModel.Cursor
	scope        *uuid.UUID
	category     inboxModel.Category
	resolvedID   uuid.UUID
}

func (f *fakeUseCase) ListInbox(
	ctx context.Context,
	userID uuid.UUID,
	scope *uuid.UUID,
	category inboxModel.Category,
	before *inboxModel.Cursor,
) (inboxModel.Page, error) {
	f.scope = scope
	f.category = category
	f.gotUserID = userID
	f.listBefore = before
	return inboxModel.Page{Items: f.list}, nil
}
func (f *fakeUseCase) MarkRead(ctx context.Context, userID, id uuid.UUID) error { return nil }
func (f *fakeUseCase) MarkAllRead(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category) error {
	f.scope = scope
	f.category = category
	return nil
}
func (f *fakeUseCase) ResolveRequest(ctx context.Context, userID, id uuid.UUID) error {
	f.gotUserID = userID
	f.resolvedID = id
	return nil
}
func (f *fakeUseCase) PollInbox(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, since *inboxModel.Cursor) (inboxModel.PollResult, error) {
	f.scope = scope
	f.gotUserID = userID
	f.pollSince = since
	return inboxModel.PollResult{
		NewInbox: []inboxModel.InAppNotification{}, UnreadCount: 2,
		Counts: inboxModel.Counts{All: 5, Requests: 2, Personal: 2, Activity: 1}, OtherEventsCount: 3,
	}, nil
}
func (f *fakeUseCase) ListBanners(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) ([]inboxModel.InAppNotification, error) {
	f.bannerUserID = userID
	return nil, nil
}
func (f *fakeUseCase) DismissBanner(ctx context.Context, userID, id uuid.UUID) error { return nil }

// fakeProt satisfies IProtection; RequirePermission is a no-op pass-through.
type fakeProt struct{}

func (fakeProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

func newEngine(uc IUseCase, userID *uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(response.WithErrorHandler)
	if userID != nil {
		engine.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: *userID, Role: rbac.RoleUser}))
			c.Next()
		})
	}
	NewInboxAPIHandler(uc, fakeProt{}).Init(engine.Group("api/notifications"))
	return engine
}

func TestListInbox_NoUser_401(t *testing.T) {
	engine := newEngine(&fakeUseCase{}, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/inbox", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListInbox_PassesUserID(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/inbox", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	assert.Contains(t, w.Body.String(), `"Items":[]`)

	id := uuid.Must(uuid.NewV7())
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?before_id="+id.String()+"&before_at=2026-09-25T12%3A00%3A00Z", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, uc.listBefore) {
		assert.Equal(t, id, uc.listBefore.ID)
		assert.Equal(t, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), uc.listBefore.CreatedAt)
	}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?before_id="+id.String(), nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListBanners_PassesUserID(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/banners", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.bannerUserID)
}

func TestPollInbox_BaselineAndCursor(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox/poll", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	assert.Nil(t, uc.pollSince)

	id := uuid.Must(uuid.NewV7())
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox/poll?since_id="+id.String()+"&since_at=2026-09-25T12%3A00%3A00Z", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, uc.pollSince) {
		assert.Equal(t, id, uc.pollSince.ID)
		assert.Equal(t, 2026, uc.pollSince.CreatedAt.Year())
	}
}

func TestPollInbox_RejectsPartialCursor(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	engine := newEngine(&fakeUseCase{}, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox/poll?since_id=abc", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestEventScope_ParsesAllForms: absent = every item, an Event id = that
// Event's scope, "none" = platform-only; list, poll and read-all agree.
func TestEventScope_ParsesAllForms(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	eventID := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/notifications/inbox"},
		{http.MethodGet, "/api/notifications/inbox/poll"},
		{http.MethodPatch, "/api/notifications/inbox/read-all"},
	} {
		uc := &fakeUseCase{}
		engine := newEngine(uc, &uid)
		call := func(query string) int {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path+query, nil))
			return w.Code
		}

		assert.Equal(t, http.StatusOK, call(""), tc.path)
		assert.Nil(t, uc.scope, tc.path)

		assert.Equal(t, http.StatusOK, call("?event="+eventID.String()), tc.path)
		if assert.NotNil(t, uc.scope, tc.path) {
			assert.Equal(t, eventID, *uc.scope, tc.path)
		}

		uc.scope = nil
		assert.Equal(t, http.StatusOK, call("?event=none"), tc.path)
		if assert.NotNil(t, uc.scope, tc.path) {
			assert.Equal(t, inboxModel.PlatformScope, *uc.scope, tc.path)
		}

		assert.Equal(t, http.StatusBadRequest, call("?event=bogus"), tc.path)
	}
}

// TestInboxCategory_ListAndReadAll: ?category= reaches list and read-all;
// an unknown tab is a 400.
func TestInboxCategory_ListAndReadAll(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/notifications/inbox"},
		{http.MethodPatch, "/api/notifications/inbox/read-all"},
	} {
		uc := &fakeUseCase{}
		engine := newEngine(uc, &uid)
		call := func(query string) int {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path+query, nil))
			return w.Code
		}
		assert.Equal(t, http.StatusOK, call(""), tc.path)
		assert.Equal(t, inboxModel.Category(""), uc.category, tc.path)
		assert.Equal(t, http.StatusOK, call("?category=requests"), tc.path)
		assert.Equal(t, inboxModel.CategoryRequests, uc.category, tc.path)
		assert.Equal(t, http.StatusBadRequest, call("?category=all"), tc.path)
	}
}

func TestPollInbox_ReturnsCounts(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	engine := newEngine(&fakeUseCase{}, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox/poll", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"Counts":{"All":5,"Requests":2,"Personal":2,"Activity":1}`)
	assert.Contains(t, w.Body.String(), `"OtherEventsCount":3`)
}

func TestListInbox_SerializesResolution(t *testing.T) {
	uid, resolver := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	resolved := time.Date(2026, 9, 29, 12, 4, 0, 0, time.UTC)
	uc := &fakeUseCase{list: []inboxModel.InAppNotification{
		{ID: uuid.Must(uuid.NewV7()), Type: "event.lab.failed", Category: inboxModel.CategoryRequests, ActionRequired: true,
			ResolvedAt: &resolved, Resolution: "fixed", ResolvedBy: &resolver, ResolvedByName: "Іван П."},
		{ID: uuid.Must(uuid.NewV7()), Type: "participant.event.finished", Category: inboxModel.CategoryActivity},
	}}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"Category":"requests","ActionRequired":true,"ResolvedAt":"2026-09-29T12:04:00Z","Resolution":"fixed","ResolvedBy":{"ID":"`+resolver.String()+`","Name":"Іван П."}`)
	assert.Contains(t, body, `"Category":"activity","ActionRequired":false,"ResolvedAt":null,"Resolution":null,"ResolvedBy":null`)
}

func TestResolveRequest_PassesCallerAndID(t *testing.T) {
	uid, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/notifications/inbox/"+id.String()+"/resolve", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	assert.Equal(t, id, uc.resolvedID)

	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/notifications/inbox/bogus/resolve", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
