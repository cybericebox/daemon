package template

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	"github.com/cybericebox/daemon/internal/model/rbac"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/tools"
)

// fakeUseCase satisfies IUseCase for handler tests.
type fakeUseCase struct {
	createdInApp inAppModel.CreateTemplateInput

	imageID        uuid.UUID
	maxImageUpload int64
	uploadErr      error
	uploaded       []byte
	uploadedName   string
	uploadedBy     uuid.UUID

	previewed   emailUseCase.PreviewInput
	previewErr  error
	realPreview bool
}

// ── in-app ──

func (f *fakeUseCase) GetInAppTemplate(
	_ context.Context,
	id uuid.UUID,
) (inAppModel.InAppTemplate, error) {
	return inAppModel.InAppTemplate{ID: id}, nil
}

func (f *fakeUseCase) CreateInAppTemplate(
	_ context.Context,
	in inAppModel.CreateTemplateInput,
) (inAppModel.InAppTemplate, error) {
	f.createdInApp = in
	return inAppModel.InAppTemplate{
		ID:               tools.NewUUIDv7(),
		NotificationType: in.NotificationType,
		Status:           "draft",
		Title:            in.Title,
		Body:             in.Body,
		Link:             in.Link,
	}, nil
}

func (f *fakeUseCase) UpdateInAppTemplate(
	_ context.Context,
	in inAppModel.UpdateTemplateInput,
) (inAppModel.InAppTemplate, error) {
	return inAppModel.InAppTemplate{ID: in.ID}, nil
}
func (f *fakeUseCase) DeleteInAppTemplate(_ context.Context, _ uuid.UUID) error { return nil }

func (f *fakeUseCase) ListInAppTemplates(
	_ context.Context,
	_ inAppModel.ListFilter,
) (inAppModel.ListResult, error) {
	return inAppModel.ListResult{}, nil
}
func (f *fakeUseCase) LatestInAppTemplates(_ context.Context) ([]inAppModel.TypeVersions, error) {
	return nil, nil
}

func (f *fakeUseCase) PublishInAppTemplate(
	_ context.Context,
	id, _ uuid.UUID,
) (inAppModel.InAppTemplate, error) {
	return inAppModel.InAppTemplate{ID: id}, nil
}

func (f *fakeUseCase) RollbackInAppTemplate(
	_ context.Context,
	id, _ uuid.UUID,
) (inAppModel.InAppTemplate, error) {
	return inAppModel.InAppTemplate{ID: id}, nil
}

// ── email ──

func (f *fakeUseCase) GetEmailTemplate(
	_ context.Context,
	id uuid.UUID,
) (emailModel.EmailTemplate, error) {
	return emailModel.EmailTemplate{ID: id}, nil
}

func (f *fakeUseCase) CreateEmailTemplate(
	_ context.Context,
	in emailModel.CreateTemplateInput,
) (emailModel.EmailTemplate, error) {
	return emailModel.EmailTemplate{
		ID:   tools.NewUUIDv7(),
		Body: json.RawMessage(`{}`),
	}, nil
}

func (f *fakeUseCase) UpdateEmailTemplate(
	_ context.Context,
	in emailModel.UpdateTemplateInput,
) (emailModel.EmailTemplate, error) {
	return emailModel.EmailTemplate{ID: in.ID}, nil
}
func (f *fakeUseCase) DeleteEmailTemplate(_ context.Context, _ uuid.UUID) error { return nil }

func (f *fakeUseCase) ListEmailTemplates(
	_ context.Context,
	_ emailModel.ListFilter,
) (emailModel.ListResult, error) {
	return emailModel.ListResult{}, nil
}
func (f *fakeUseCase) LatestEmailTemplates(_ context.Context) ([]emailModel.TypeVersions, error) {
	return nil, nil
}

func (f *fakeUseCase) PublishEmailTemplate(
	_ context.Context,
	id, _ uuid.UUID,
) (emailModel.EmailTemplate, error) {
	return emailModel.EmailTemplate{ID: id}, nil
}

func (f *fakeUseCase) RollbackEmailTemplate(
	_ context.Context,
	id, _ uuid.UUID,
) (emailModel.EmailTemplate, error) {
	return emailModel.EmailTemplate{ID: id}, nil
}

// ── presets ──

func (f *fakeUseCase) ListEmailBlockPresets(_ context.Context) ([]emailModel.BlockPreset, error) {
	return nil, nil
}

func (f *fakeUseCase) GetEmailBlockPreset(
	_ context.Context,
	id uuid.UUID,
) (emailModel.BlockPreset, error) {
	return emailModel.BlockPreset{ID: id}, nil
}

func (f *fakeUseCase) CreateEmailBlockPreset(
	_ context.Context,
	in emailModel.PresetInput,
) (emailModel.BlockPreset, error) {
	return emailModel.BlockPreset{ID: tools.NewUUIDv7(), Name: in.Name}, nil
}

func (f *fakeUseCase) UpdateEmailBlockPreset(
	_ context.Context,
	id uuid.UUID,
	in emailModel.PresetInput,
) (emailModel.BlockPreset, error) {
	return emailModel.BlockPreset{ID: id, Name: in.Name}, nil
}
func (f *fakeUseCase) DeleteEmailBlockPreset(_ context.Context, _ uuid.UUID) error { return nil }

// ── protection helpers ──

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

// ── engine builders ──

// withUserID injects a known admin UUID into request context so handlers that
// call rbac.UserIDFromContext succeed in tests.
func withUserID(id uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: id, Role: rbac.RoleUser})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func newEngine(uc IUseCase) *gin.Engine {
	return newEngineWithProt(uc, fakeProt{})
}

func newEngineWithProt(uc IUseCase, prot IProtection) *gin.Engine {
	gin.SetMode(gin.TestMode)
	adminID := tools.NewUUIDv7()
	engine := gin.New()
	engine.Use(response.WithErrorHandler)
	engine.Use(withUserID(adminID))
	NewTemplateAPIHandler(uc, prot).Init(engine.Group("api/notifications"))
	return engine
}

// ── tests ──

func TestCreateInAppTemplate_MapsBody(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngine(uc)
	body := `{"NotificationType":"flag_accepted","Title":"Hi","Body":"B","Link":"L"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/notifications/templates/inapp",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "flag_accepted", uc.createdInApp.NotificationType)
	assert.Equal(t, "Hi", uc.createdInApp.Title)
}

// TestCreateInAppTemplate_DeniedByProt_403 proves the POST templates/inapp route
// (templates.write) is gated: when RequirePermission denies, the handler never runs.
func TestCreateInAppTemplate_DeniedByProt_403(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngineWithProt(uc, denyProt{})
	body := `{"NotificationType":"flag_accepted","Title":"Hi","Body":"B","Link":"L"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/notifications/templates/inapp",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, uc.createdInApp.NotificationType) // handler body did not execute
}

func TestListEmailTemplates_OK(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngine(uc)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/templates/email", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublishEmailTemplate_OK(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngine(uc)
	id := tools.NewUUIDv7()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/notifications/templates/email/"+id.String()+"/publish",
		nil,
	)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListEmailBlockPresets_OK(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngine(uc)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/notifications/templates/email/block-presets",
		nil,
	)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
