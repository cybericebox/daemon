package inbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type fakeUseCase struct {
	list        []inboxModel.InAppNotification
	gotUserID   uuid.UUID
	listBefore  *inboxModel.Cursor
	sinceCursor *inboxModel.Cursor
	scope       *uuid.UUID
	category    inboxModel.Category
	resolvedID  uuid.UUID
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
func (f *fakeUseCase) InboxSince(_ context.Context, userID uuid.UUID, scope *uuid.UUID, since *inboxModel.Cursor) (inboxModel.SinceResult, error) {
	f.scope = scope
	f.gotUserID = userID
	f.sinceCursor = since
	return inboxModel.SinceResult{
		NewInbox: []inboxModel.InAppNotification{}, UnreadCount: 2,
		Counts: inboxModel.Counts{All: 5, Requests: 2, Personal: 2, Activity: 1}, OtherEventsCount: 3,
	}, nil
}

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
	hosts := config.HostsConfig{Main: "example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", API: "api.example.test", EventDomain: "example.test"}
	site := middleware.NewSiteResolver(hosts, func(context.Context, string) (uuid.UUID, error) { return uuid.Nil, eventModel.ErrEventNotFound.Err() }).ResolveSite()
	NewInboxAPIHandler(uc, fakeProt{}, site).Init(engine.Group("api/notifications"))
	return engine
}

func TestListInbox_NoUser_401(t *testing.T) {
	engine := newEngine(&fakeUseCase{}, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListInbox_PassesUserID(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	assert.Contains(t, w.Body.String(), `"Items":[]`)

	id := uuid.Must(uuid.NewV7())
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&before="+inboxModel.Cursor{ID: id, CreatedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, uc.listBefore) {
		assert.Equal(t, id, uc.listBefore.ID)
		assert.Equal(t, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), uc.listBefore.CreatedAt)
	}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&before="+id.String(), nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestInboxSince_BaselineAndCursor(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&since=", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	assert.Nil(t, uc.sinceCursor)
	assert.Contains(t, w.Body.String(), `"Unread":2`)

	id := uuid.Must(uuid.NewV7())
	cursor := inboxModel.Cursor{ID: id, CreatedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&since="+cursor.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
	if assert.NotNil(t, uc.sinceCursor) {
		assert.Equal(t, id, uc.sinceCursor.ID)
		assert.Equal(t, 2026, uc.sinceCursor.CreatedAt.Year())
	}
}

func TestInboxSince_RejectsBadCursorAndMixedParams(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	engine := newEngine(&fakeUseCase{}, &uid)
	for _, query := range []string{"since=abc", "since=&category=requests", "since=&before=x", "before=abc"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&"+query, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, query)
	}
}

func TestListInbox_NextCursorIsOneString(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	cursor := inboxModel.Cursor{ID: uuid.Must(uuid.NewV7()), CreatedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	got, err := inboxModel.ParseCursor(cursor.String())
	require.NoError(t, err)
	assert.Equal(t, cursor, *got)
	engine := newEngine(&fakeUseCase{}, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&before="+cursor.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"NextCursor":""`)
}

// TestSiteScope: the scope comes from the site: an event site lists that event, every other site only
// the platform items; the site is the query parameter or the Origin.
func TestSiteScope(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	eventID := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/notifications/inbox"},
		{http.MethodGet, "/api/notifications/inbox?since="},
		{http.MethodPatch, "/api/notifications/inbox/read-all"},
	} {
		uc := &fakeUseCase{}
		engine := newEngine(uc, &uid)
		call := func(query, origin string) int {
			w := httptest.NewRecorder()
			sep := "?"
			if strings.Contains(tc.path, "?") {
				sep = "&"
			}
			req := httptest.NewRequest(tc.method, tc.path+sep+query, nil)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			engine.ServeHTTP(w, req)
			return w.Code
		}

		assert.Equal(t, http.StatusOK, call("site=event&event="+eventID.String(), ""), tc.path)
		if assert.NotNil(t, uc.scope, tc.path) {
			assert.Equal(t, eventID, *uc.scope, tc.path)
		}

		uc.scope = nil
		assert.Equal(t, http.StatusOK, call("site=admin", ""), tc.path)
		if assert.NotNil(t, uc.scope, tc.path) {
			assert.Equal(t, inboxModel.PlatformScope, *uc.scope, tc.path)
		}

		uc.scope = nil
		assert.Equal(t, http.StatusOK, call("", "https://id.example.test"), tc.path)
		if assert.NotNil(t, uc.scope, tc.path) {
			assert.Equal(t, inboxModel.PlatformScope, *uc.scope, tc.path)
		}

		assert.Equal(t, http.StatusBadRequest, call("", ""), tc.path)
		assert.Equal(t, http.StatusBadRequest, call("site=bogus", ""), tc.path)
		assert.Equal(t, http.StatusBadRequest, call("site=event", ""), tc.path)
		assert.Equal(t, http.StatusBadRequest, call("site=main&event="+eventID.String(), ""), tc.path)
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
			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path+"?site=main&"+strings.TrimPrefix(query, "?"), nil))
			return w.Code
		}
		assert.Equal(t, http.StatusOK, call(""), tc.path)
		assert.Equal(t, inboxModel.Category(""), uc.category, tc.path)
		assert.Equal(t, http.StatusOK, call("?category=requests"), tc.path)
		assert.Equal(t, inboxModel.CategoryRequests, uc.category, tc.path)
		assert.Equal(t, http.StatusBadRequest, call("?category=all"), tc.path)
	}
}

func TestInboxSince_ReturnsCounts(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	engine := newEngine(&fakeUseCase{}, &uid)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main&since=", nil))
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
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"Category":"requests","ActionRequired":true,"ResolvedAt":"2026-09-29T12:04:00Z","Resolution":"fixed","ResolvedBy":{"ID":"`+resolver.String()+`","Name":"Іван П."},"Resolvable":false`)
	assert.Contains(t, body, `"Category":"activity","ActionRequired":false,"ResolvedAt":null,"Resolution":null,"ResolvedBy":null,"Resolvable":false`)
}

// Resolvable follows the server's own rule: an open request of a manually resolvable type, nothing else.
func TestListInbox_ResolvableFlag(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	resolved := time.Date(2026, 10, 19, 14, 25, 0, 0, time.UTC)
	uc := &fakeUseCase{list: []inboxModel.InAppNotification{
		{ID: uuid.Must(uuid.NewV7()), Type: "resources.alarm.raised", Category: inboxModel.CategoryRequests, ActionRequired: true, Title: "open alarm"},
		{ID: uuid.Must(uuid.NewV7()), Type: "resources.alarm.raised", Category: inboxModel.CategoryRequests, ActionRequired: true, ResolvedAt: &resolved, Title: "closed alarm"},
		{ID: uuid.Must(uuid.NewV7()), Type: "event.application.submitted", Category: inboxModel.CategoryRequests, ActionRequired: true, Title: "decision only"},
	}}
	w := httptest.NewRecorder()
	newEngine(uc, &uid).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/inbox?site=main", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Regexp(t, `"Title":"open alarm".*?"Resolvable":true`, body)
	assert.Regexp(t, `"Title":"closed alarm".*?"Resolvable":false`, body)
	assert.Regexp(t, `"Title":"decision only".*?"Resolvable":false`, body)
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
