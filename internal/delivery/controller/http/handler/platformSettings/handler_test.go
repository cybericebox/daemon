package platformSettings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformSettingsUseCase "github.com/cybericebox/daemon/internal/useCase/platformSettings"
)

// fakeUseCase satisfies IUseCase; GetSetting returns a preset error so the test
// can drive the not-found path through the real error-handling middleware.
type fakeUseCase struct {
	getErr error
	got    *platformSettingsModel.PlatformSetting
}

func (f *fakeUseCase) GetPlatformSetting(
	ctx context.Context,
	key string,
) (*platformSettingsModel.PlatformSetting, error) {
	return f.got, f.getErr
}

func (f *fakeUseCase) GetPlatformSettingValue(
	ctx context.Context,
	key string,
) (json.RawMessage, error) {
	s, err := f.GetPlatformSetting(ctx, key)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	return s.Value, nil
}

func (f *fakeUseCase) ListPlatformSettings(
	ctx context.Context,
) ([]platformSettingsModel.PlatformSetting, error) {
	return nil, nil
}
func (f *fakeUseCase) UpsertPlatformSetting(ctx context.Context, in platformSettingsUseCase.UpsertInput) (
	*platformSettingsModel.PlatformSetting,
	error,
) {
	if f.got != nil {
		return f.got, nil
	}
	return &platformSettingsModel.PlatformSetting{Key: in.Key, Value: in.Value}, nil
}

// fakeProt is a controllable IProtection. When allow==true the middleware
// passes through; when allow==false it aborts with 403 Forbidden.
type fakeProt struct {
	allow bool
}

func (fp *fakeProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	if fp.allow {
		return func(ctx *gin.Context) { ctx.Next() }
	}
	return func(ctx *gin.Context) {
		ctx.AbortWithStatus(http.StatusForbidden)
	}
}

// newEngine wires the setting handler behind the real WithErrorHandler middleware,
// so a domain error returned by the useCase maps to its HTTP status via the lib
// error's StatusCode — exactly as in production.
func newEngine(uc IUseCase, prot IProtection) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(response.WithErrorHandler)
	NewSettingAPIHandler(uc, prot).Init(engine.Group("api"))
	return engine
}

func TestGetSetting_NotFoundMapsTo404(t *testing.T) {
	engine := newEngine(&fakeUseCase{getErr: platformSettingsModel.ErrSettingNotFound.Err()}, &fakeProt{allow: true})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/x", nil)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpsertSetting_WithoutPermission_Returns403(t *testing.T) {
	engine := newEngine(&fakeUseCase{}, &fakeProt{allow: false})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/somekey", bytes.NewBufferString(`{"Value":{}}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpsertSetting_WithPermission_Returns200(t *testing.T) {
	engine := newEngine(&fakeUseCase{}, &fakeProt{allow: true})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/somekey", bytes.NewBufferString(`{"Value":{}}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
