package imageRegistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

var (
	evA    = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-00000000000a"))
	evB    = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-00000000000b"))
	secUID = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000e1"))
	userID = uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000f1"))
)

// Event access: the user manages event A, only reads event B.
func (f *fakeUseCase) RequireManageEvent(_ context.Context, eventID, _ uuid.UUID) error {
	if eventID != evA {
		return imageRegistryModel.ErrEventSecretNotFound.Err()
	}
	return nil
}

func (f *fakeUseCase) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeUseCase) secretView() imageRegistryUseCase.EventSecretView {
	return imageRegistryUseCase.EventSecretView{
		ID: secUID.String(), Host: "ghcr.io", RepositoryPrefix: "acme", Username: "bob",
		UpdatedAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC),
	}
}

func (f *fakeUseCase) ListEventRegistrySecrets(_ context.Context, eventID uuid.UUID) ([]imageRegistryUseCase.EventSecretView, error) {
	f.secretEvent = eventID
	return []imageRegistryUseCase.EventSecretView{f.secretView()}, nil
}

func (f *fakeUseCase) CreateEventRegistrySecret(_ context.Context, eventID uuid.UUID, in imageRegistryUseCase.EventSecretInput) (imageRegistryUseCase.EventSecretView, error) {
	f.secretEvent, f.secretInput = eventID, in
	return f.secretView(), nil
}

func (f *fakeUseCase) UpdateEventRegistrySecret(_ context.Context, eventID, secretID uuid.UUID, in imageRegistryUseCase.EventSecretInput) (imageRegistryUseCase.EventSecretView, error) {
	f.secretEvent, f.secretID, f.secretInput = eventID, secretID, in
	return f.secretView(), nil
}

func (f *fakeUseCase) DeleteEventRegistrySecret(_ context.Context, eventID, secretID uuid.UUID) error {
	f.secretEvent, f.secretID = eventID, secretID
	f.deletes++
	return nil
}

func secretsEngine(uc IUseCase) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(response.WithErrorHandler, func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: userID}))
	})
	New(uc, fakeProt{granted: map[rbac.Permission]bool{rbac.PermSelf: true}}).Init(engine.Group("api"))
	return engine
}

func TestEventSecretRoutes(t *testing.T) {
	uc := &fakeUseCase{}
	engine := secretsEngine(uc)
	base := "/api/events/" + evA.String() + "/manage/registry-secrets"
	body := `{"Host":"ghcr.io","RepositoryPrefix":"acme","Username":"bob","Token":"` + secretToken + `"}`

	w := do(engine, http.MethodPost, base, body)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secretToken) || uc.secretInput.Token != secretToken || uc.secretEvent != evA {
		t.Fatalf("create: %d %s %+v", w.Code, w.Body, uc.secretInput)
	}
	w = do(engine, http.MethodPut, base+"/"+secUID.String(), body)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secretToken) || uc.secretID != secUID {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	w = do(engine, http.MethodGet, base, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), secretToken) || strings.Contains(strings.ToLower(w.Body.String()), "token") ||
		!strings.Contains(w.Body.String(), `"Host":"ghcr.io"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w = do(engine, http.MethodDelete, base+"/"+secUID.String(), ""); w.Code != http.StatusOK || uc.deletes != 1 {
		t.Fatalf("delete: %d", w.Code)
	}
}

func TestEventSecretWritesNeedManage(t *testing.T) {
	uc := &fakeUseCase{}
	engine := secretsEngine(uc)
	base := "/api/events/" + evB.String() + "/manage/registry-secrets"
	body := `{"Host":"ghcr.io","Username":"bob","Token":"x"}`
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, base}, {http.MethodPut, base + "/" + secUID.String()}, {http.MethodDelete, base + "/" + secUID.String()},
	} {
		if w := do(engine, c.method, c.path, body); w.Code != http.StatusNotFound || uc.deletes != 0 || uc.secretInput.Token != "" {
			t.Errorf("%s %s without manage: %d", c.method, c.path, w.Code)
		}
	}
	if w := do(engine, http.MethodGet, base, ""); w.Code != http.StatusOK {
		t.Errorf("read: %d", w.Code)
	}
}

func TestEventSecretBadIDs(t *testing.T) {
	engine := secretsEngine(&fakeUseCase{})
	for _, p := range []string{
		"/api/events/nope/manage/registry-secrets/" + secUID.String(),
		"/api/events/" + evA.String() + "/manage/registry-secrets/nope",
	} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodPut, p, strings.NewReader(`{"Host":"ghcr.io","Username":"bob"}`)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

// archivedUseCase is the use case of an archived event: it implements the
// middleware's EventWriteGuard and refuses every write.
type archivedUseCase struct{ *fakeUseCase }

func (archivedUseCase) RequireEventWritable(context.Context, uuid.UUID) error {
	return imageRegistryModel.ErrEventSecretNotFound.Err()
}

func TestArchivedEventRefusesSecretWrites(t *testing.T) {
	uc := &fakeUseCase{}
	engine := secretsEngine(archivedUseCase{uc})
	base := "/api/events/" + evA.String() + "/manage/registry-secrets"
	body := `{"Host":"ghcr.io","Username":"bob","Token":"x"}`
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, base}, {http.MethodPut, base + "/" + secUID.String()}, {http.MethodDelete, base + "/" + secUID.String()},
	} {
		if w := do(engine, c.method, c.path, body); w.Code == http.StatusOK {
			t.Errorf("%s %s on an archived event: %d", c.method, c.path, w.Code)
		}
	}
	if uc.deletes != 0 || uc.secretInput.Token != "" || uc.secretID != uuid.Nil {
		t.Fatalf("a write reached the use case: %+v", uc)
	}
	if w := do(engine, http.MethodGet, base, ""); w.Code != http.StatusOK {
		t.Errorf("read on an archived event: %d", w.Code)
	}
}
