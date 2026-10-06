package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type fakeUseCase struct {
	gotUserID     uuid.UUID
	gotType       notificationTypes.NotificationType
	gotVars       map[string]any
	gotChannels   []notificationTypes.NotificationChannel
	gotTemplateID *uuid.UUID
}

func (f *fakeUseCase) Notify(
	ctx context.Context,
	userID uuid.UUID,
	n notificationTypes.NotificationPayload,
	opts ...dispatchModel.NotifyOption,
) error {
	f.gotUserID = userID
	f.gotType = n.NotificationType()
	raw, _ := n.Marshal()
	_ = raw
	o := dispatchModel.ApplyNotifyOptions(opts)
	f.gotChannels = o.OverrideChannels
	f.gotTemplateID = o.TemplateID
	return nil
}

// fakeProt satisfies IProtection; RequirePermission is a no-op pass-through.
type fakeProt struct{}

func (fakeProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

// denyProt satisfies IProtection; RequirePermission aborts 403.
type denyProt struct{}

func (denyProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) }
}

func newEngine(uc IUseCase, userID *uuid.UUID) *gin.Engine {
	return newEngineWithProt(uc, userID, fakeProt{})
}

func newEngineWithProt(uc IUseCase, userID *uuid.UUID, prot IProtection) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(response.WithErrorHandler)
	if userID != nil {
		engine.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: *userID, Role: rbac.RoleUser}))
			c.Next()
		})
	}
	NewTestAPIHandler(uc, prot).Init(engine.Group("api/notifications"))
	return engine
}

func TestTestSend_NoUser_401(t *testing.T) {
	engine := newEngine(&fakeUseCase{}, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/notifications/test",
		strings.NewReader(`{"Type":"flag_accepted","Channels":["in_app"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTestSend_PassesTypeChannelsAndUser(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	body := `{"Type":"flag_accepted","Channels":["in_app","email"],"Variables":{"Challenge":"X","Points":5}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	assert.Equal(t, notificationTypes.NotificationTypeFlagAccepted, uc.gotType)
	assert.Equal(t, []notificationTypes.NotificationChannel{
		notificationTypes.NotificationChannelInApp,
		notificationTypes.NotificationChannelEmail,
	}, uc.gotChannels)
}

// TestTestSend_WithTemplateID_PassedAsOption asserts that when TemplateID is present in
// the request body, the handler returns 200 and the WithTemplateID option carries the
// same UUID into Notify (captured by fakeUseCase.gotTemplateID).
func TestTestSend_WithTemplateID_PassedAsOption(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	templateID := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	body := `{"Type":"flag_accepted","Channels":["in_app"],"TemplateID":"` + templateID.String() + `"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUserID)
	if assert.NotNil(t, uc.gotTemplateID, "WithTemplateID option must carry the UUID") {
		assert.Equal(t, templateID, *uc.gotTemplateID)
	}
}

// TestTestSend_DeniedByProt_403 proves the POST test route (notifications.test)
// is gated: when RequirePermission denies, the handler never runs.
func TestTestSend_DeniedByProt_403(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngineWithProt(uc, &uid, denyProt{})
	body := `{"Type":"flag_accepted","Channels":["in_app"]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, uuid.Nil, uc.gotUserID) // handler body did not execute
}
