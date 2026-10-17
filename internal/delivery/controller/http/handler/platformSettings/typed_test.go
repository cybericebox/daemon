package platformSettings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformSettingsUseCase "github.com/cybericebox/daemon/internal/useCase/platformSettings"
)

var typedUpdatedAt = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func sampleTypedView() platformSettingsUseCase.TypedView {
	return platformSettingsUseCase.TypedView{Custom: true, Ignored: true, UpdatedAt: &typedUpdatedAt, Fields: []platformSettingsUseCase.FieldView{
		{Key: "Lease", Kind: "duration", Value: 3600, Default: 7200, Min: 3600, Max: 86400},
	}}
}

// The typed methods of fakeUseCase record the call and answer with one view.
func (f *fakeUseCase) typed(call string, in platformSettingsModel.Values) (platformSettingsUseCase.TypedView, error) {
	f.typedCalls = append(f.typedCalls, call)
	f.typedIn = in
	return sampleTypedView(), f.typedErr
}

func (f *fakeUseCase) GetRetentionSettings(context.Context) (platformSettingsUseCase.TypedView, error) {
	return f.typed("get retention", nil)
}
func (f *fakeUseCase) SaveRetentionSettings(_ context.Context, in platformSettingsModel.Values) (platformSettingsUseCase.TypedView, error) {
	return f.typed("save retention", in)
}
func (f *fakeUseCase) ResetRetentionSettings(context.Context) (platformSettingsUseCase.TypedView, error) {
	return f.typed("reset retention", nil)
}
func (f *fakeUseCase) GetArchiveLimitSettings(context.Context) (platformSettingsUseCase.TypedView, error) {
	return f.typed("get archive", nil)
}
func (f *fakeUseCase) SaveArchiveLimitSettings(_ context.Context, in platformSettingsModel.Values) (platformSettingsUseCase.TypedView, error) {
	return f.typed("save archive", in)
}
func (f *fakeUseCase) ResetArchiveLimitSettings(context.Context) (platformSettingsUseCase.TypedView, error) {
	return f.typed("reset archive", nil)
}
func (f *fakeUseCase) GetTestRunSettings(context.Context) (platformSettingsUseCase.TypedView, error) {
	return f.typed("get test runs", nil)
}
func (f *fakeUseCase) SaveTestRunSettings(_ context.Context, in platformSettingsModel.Values) (platformSettingsUseCase.TypedView, error) {
	return f.typed("save test runs", in)
}
func (f *fakeUseCase) ResetTestRunSettings(context.Context) (platformSettingsUseCase.TypedView, error) {
	return f.typed("reset test runs", nil)
}

// grantProt grants one permission and refuses the rest with 403.
type grantProt struct{ granted rbac.Permission }

func (p grantProt) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if required != p.granted {
			ctx.AbortWithStatus(http.StatusForbidden)
			return
		}
		ctx.Next()
	}
}

func serve(engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	return w
}

func TestTypedRoutes_Gated(t *testing.T) {
	body := `{"Values":{"Lease":3600}}`
	routes := []struct {
		method, path, body, call string
		perm                     rbac.Permission
	}{
		{http.MethodGet, "/api/settings/retention", "", "get retention", rbac.PermPlatformSettingsRead},
		{http.MethodPut, "/api/settings/retention", body, "save retention", rbac.PermPlatformSettingsWrite},
		{http.MethodDelete, "/api/settings/retention", "", "reset retention", rbac.PermPlatformSettingsWrite},
		{http.MethodGet, "/api/settings/archive-limits", "", "get archive", rbac.PermPlatformSettingsRead},
		{http.MethodPut, "/api/settings/archive-limits", body, "save archive", rbac.PermPlatformSettingsWrite},
		{http.MethodDelete, "/api/settings/archive-limits", "", "reset archive", rbac.PermPlatformSettingsWrite},
		{http.MethodGet, "/api/settings/test-runs", "", "get test runs", rbac.PermPlatformSettingsRead},
		{http.MethodPut, "/api/settings/test-runs", body, "save test runs", rbac.PermPlatformSettingsWrite},
		{http.MethodDelete, "/api/settings/test-runs", "", "reset test runs", rbac.PermPlatformSettingsWrite},
	}
	for _, r := range routes {
		denied := rbac.PermPlatformSettingsRead
		if r.perm == rbac.PermPlatformSettingsRead {
			denied = rbac.PermPlatformSettingsWrite
		}
		if w := serve(newEngine(&fakeUseCase{}, grantProt{granted: denied}), r.method, r.path, r.body); w.Code != http.StatusForbidden {
			t.Errorf("%s %s with only %s: %d", r.method, r.path, denied, w.Code)
		}
		uc := &fakeUseCase{}
		if w := serve(newEngine(uc, grantProt{granted: r.perm}), r.method, r.path, r.body); w.Code != http.StatusOK {
			t.Errorf("%s %s with %s: %d %s", r.method, r.path, r.perm, w.Code, w.Body)
		}
		if len(uc.typedCalls) != 1 || uc.typedCalls[0] != r.call {
			t.Errorf("%s %s reached %v, want %q", r.method, r.path, uc.typedCalls, r.call)
		}
	}
}

func TestTypedGet_ResponseShape(t *testing.T) {
	w := serve(newEngine(&fakeUseCase{}, &fakeProt{allow: true}), http.MethodGet, "/api/settings/test-runs", "")
	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Data map[string]any
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got.Data["Custom"])
	assert.Equal(t, true, got.Data["Ignored"])
	assert.Equal(t, "2026-10-18T12:00:00Z", got.Data["UpdatedAt"])
	fields := got.Data["Fields"].([]any)
	require.Len(t, fields, 1)
	assert.Equal(t, map[string]any{
		"Key": "Lease", "Kind": "duration", "Value": float64(3600), "Default": float64(7200),
		"Min": float64(3600), "Max": float64(86400), "InPrivacyPolicy": false,
	}, fields[0])
}

func TestTypedPut_PassesTheValues(t *testing.T) {
	uc := &fakeUseCase{}
	w := serve(newEngine(uc, &fakeProt{allow: true}), http.MethodPut, "/api/settings/test-runs", `{"Values":{"Lease":3600,"LeaseMax":7200}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, platformSettingsModel.Values{"Lease": 3600, "LeaseMax": 7200}, uc.typedIn)
}

func TestTypedPut_NonIntegerNumber_Is400(t *testing.T) {
	for _, body := range []string{
		`{"Values":{"Lease":1.5}}`,
		`{"Values":{"Lease":"3600"}}`,
		`{"Values":{"Lease":1e3}}`,
		`{"Values":{"Lease":99999999999999999999}}`,
		`{"Values":[3600]}`,
	} {
		uc := &fakeUseCase{}
		w := serve(newEngine(uc, &fakeProt{allow: true}), http.MethodPut, "/api/settings/test-runs", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, body)
		assert.Empty(t, uc.typedCalls, "%s must not reach the use case", body)
	}
}

func TestTypedPut_DomainRefusalKeepsTheField(t *testing.T) {
	uc := &fakeUseCase{typedErr: platformSettingsModel.ErrTestLeaseOverMax.WithPublicContext("field", "LeaseMax").Err()}
	w := serve(newEngine(uc, &fakeProt{allow: true}), http.MethodPut, "/api/settings/test-runs", `{"Values":{"Lease":7200,"LeaseMax":3600}}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var got response.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "LeaseMax", got.Status.Context["field"])
}
