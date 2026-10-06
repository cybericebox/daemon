package settings

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
	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type fakeUseCase struct {
	gotUser          settingsModel.UpsertUserInput
	gotSignalDefault settingsModel.SignalDefault
}

func (f *fakeUseCase) ListGlobalSettings(
	ctx context.Context,
) ([]settingsModel.GlobalSetting, error) {
	return nil, nil
}

func (f *fakeUseCase) UpsertGlobalSetting(
	ctx context.Context,
	in settingsModel.UpsertGlobalInput,
) (settingsModel.GlobalSetting, error) {
	return settingsModel.GlobalSetting{
		NotificationType: in.NotificationType,
		Channel:          in.Channel,
		Enabled:          in.Enabled,
	}, nil
}

func (f *fakeUseCase) ListUserSettings(
	ctx context.Context,
	userID uuid.UUID,
) ([]settingsModel.UserSetting, error) {
	return nil, nil
}

func (f *fakeUseCase) UpsertUserSetting(
	ctx context.Context,
	in settingsModel.UpsertUserInput,
) error {
	f.gotUser = in
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
	NewSettingsAPIHandler(uc, prot).Init(engine.Group("api/notifications"))
	return engine
}

func TestUpsertUserSetting_UsesContextUserID(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)
	body := `{"NotificationType":"flag_accepted","Channel":"in_app","Enabled":true}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/notifications/settings/user",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, uc.gotUser.UserID)
	assert.Equal(t, "flag_accepted", uc.gotUser.NotificationType)
}

// TestUpsertGlobal_DeniedByProt_403 proves the PUT settings/global route
// (settings.write) is gated: when RequirePermission denies, the handler never runs.
func TestUpsertGlobal_DeniedByProt_403(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	engine := newEngineWithProt(&fakeUseCase{}, &uid, denyProt{})
	body := `{"NotificationType":"flag_accepted","Channel":"in_app","Enabled":true}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/notifications/settings/global",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
