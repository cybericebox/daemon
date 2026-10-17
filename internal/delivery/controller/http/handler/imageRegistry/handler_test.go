package imageRegistry

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
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

const secretToken = "dckr_pat_fixed_secret"

type fakeUseCase struct {
	saved  imageRegistryUseCase.SaveSettingsInput
	listed imageRegistryUseCase.ListRepositoriesInput
	clears int

	secretEvent, secretID uuid.UUID
	secretInput           imageRegistryUseCase.EventSecretInput
	deletes               int
	availabilityEvent     uuid.UUID
}

func (f *fakeUseCase) view() imageRegistryUseCase.SettingsView {
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	return imageRegistryUseCase.SettingsView{
		Configured: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
		PushTokenSet: true, UpdatedAt: &at,
	}
}

func (f *fakeUseCase) GetImageRegistrySettings(context.Context) (imageRegistryUseCase.SettingsView, error) {
	return f.view(), nil
}

func (f *fakeUseCase) SaveImageRegistrySettings(_ context.Context, in imageRegistryUseCase.SaveSettingsInput) (imageRegistryUseCase.SettingsView, error) {
	f.saved = in
	return f.view(), nil
}

func (f *fakeUseCase) ListImageRegistryRepositories(_ context.Context, in imageRegistryUseCase.ListRepositoriesInput) ([]string, error) {
	f.listed = in
	return []string{"acme/platform"}, nil
}

func (f *fakeUseCase) ClearImageRegistrySettings(context.Context) error {
	f.clears++
	return nil
}

// fakeProt grants a fixed permission set and refuses the rest with 403.
type fakeProt struct{ granted map[rbac.Permission]bool }

func (p fakeProt) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !p.granted[required] {
			ctx.AbortWithStatus(http.StatusForbidden)
			return
		}
		ctx.Next()
	}
}

func newEngine(uc IUseCase, granted ...rbac.Permission) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(response.WithErrorHandler)
	g := map[rbac.Permission]bool{}
	for _, p := range granted {
		g[p] = true
	}
	New(uc, fakeProt{granted: g}).Init(engine.Group("api"))
	return engine
}

func do(engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	return w
}

func TestRouteGates(t *testing.T) {
	saveBody := `{"Provider":"dockerhub","Account":"acme","Repository":"acme/platform","PushToken":"` + secretToken + `"}`
	listBody := `{"Provider":"dockerhub","Account":"acme","Token":"` + secretToken + `"}`
	routes := []struct {
		method, path, body string
		perm               rbac.Permission
	}{
		{http.MethodGet, "/api/settings/image-registry", "", rbac.PermPlatformSettingsRead},
		{http.MethodPut, "/api/settings/image-registry", saveBody, rbac.PermPlatformSettingsWrite},
		{http.MethodDelete, "/api/settings/image-registry", "", rbac.PermPlatformSettingsWrite},
		{http.MethodPost, "/api/settings/image-registry/repositories", listBody, rbac.PermPlatformSettingsWrite},
	}
	for _, r := range routes {
		if w := do(newEngine(&fakeUseCase{}), r.method, r.path, r.body); w.Code != http.StatusForbidden {
			t.Errorf("%s %s without permission: %d", r.method, r.path, w.Code)
		}
		other := rbac.PermPlatformSettingsRead
		if r.perm == rbac.PermPlatformSettingsRead {
			other = rbac.PermPlatformSettingsWrite
		}
		if w := do(newEngine(&fakeUseCase{}, other), r.method, r.path, r.body); w.Code != http.StatusForbidden {
			t.Errorf("%s %s with %s only: %d", r.method, r.path, other, w.Code)
		}
		if w := do(newEngine(&fakeUseCase{}, r.perm), r.method, r.path, r.body); w.Code != http.StatusOK {
			t.Errorf("%s %s with %s: %d %s", r.method, r.path, r.perm, w.Code, w.Body)
		}
	}
}

func TestResponsesNeverCarryTheToken(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngine(uc, rbac.PermPlatformSettingsRead, rbac.PermPlatformSettingsWrite)

	w := do(engine, http.MethodPut, "/api/settings/image-registry",
		`{"Provider":"dockerhub","Account":"acme","Repository":"acme/platform","PushToken":"`+secretToken+`","PullToken":"`+secretToken+`-pull","ClearPullToken":false}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secretToken) {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if uc.saved.PushToken != secretToken || uc.saved.PullToken != secretToken+"-pull" || uc.saved.Repository != "acme/platform" {
		t.Fatalf("input %+v", uc.saved)
	}
	if !strings.Contains(w.Body.String(), `"PushTokenSet":true`) || !strings.Contains(w.Body.String(), `"SecretsKeyMissing":false`) {
		t.Fatalf("save body %s", w.Body)
	}

	w = do(engine, http.MethodGet, "/api/settings/image-registry", "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secretToken) || strings.Contains(w.Body.String(), "Ciphertext") {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}

	w = do(engine, http.MethodPost, "/api/settings/image-registry/repositories", `{"Provider":"ghcr","Account":"acme","Token":"`+secretToken+`"}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secretToken) || !strings.Contains(w.Body.String(), "acme/platform") {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if uc.listed.Token != secretToken || uc.listed.Provider != "ghcr" {
		t.Fatalf("list input %+v", uc.listed)
	}

	if w = do(engine, http.MethodDelete, "/api/settings/image-registry", ""); w.Code != http.StatusOK || uc.clears != 1 {
		t.Fatalf("delete: %d clears %d", w.Code, uc.clears)
	}
}
