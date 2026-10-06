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
	"github.com/stretchr/testify/require"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/internal/model/rbac"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// ── fakeUseCase: preview ──

func (f *fakeUseCase) PreviewEmail(_ context.Context, in emailUseCase.PreviewInput) (emailUseCase.PreviewOutput, error) {
	f.previewed = in
	if f.realPreview {
		return emailUseCase.Preview(context.Background(), noPresets{}, nil, nil, nil, in)
	}
	if f.previewErr != nil {
		return emailUseCase.PreviewOutput{}, f.previewErr
	}
	return emailUseCase.PreviewOutput{
		Subject:   "S:" + in.Subject,
		Preheader: "P:" + in.Preheader,
		HTML:      "<p>" + f.imageID.String() + "</p>",
	}, nil
}

// noPresets is an empty emailUseCase.PresetLister.
type noPresets struct{}

func (noPresets) ListPresets(context.Context) ([]emailModel.BlockPreset, error) { return nil, nil }

// onlyProt grants exactly one permission and denies every other one with 403.
type onlyProt struct{ allow rbac.Permission }

func (p onlyProt) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if required != p.allow {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

func postPreview(engine *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/templates/email/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	return w
}

func TestPreviewEmail_MapsRequestAndResponse(t *testing.T) {
	uc := &fakeUseCase{imageID: uuid.Must(uuid.NewV7())}
	engine := newEngineWithProt(uc, onlyProt{allow: rbac.PermNotificationsTemplatesRead})

	w := postPreview(engine, `{"NotificationType":"participant.approval_registration.approved","Subject":"Hi","Preheader":"pre",`+
		`"Body":[{"type":"logo"}],"Styling":{"cta_bg_color":"theme:brand"},"Values":{"user_first_name":"Ada"}}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "participant.approval_registration.approved", uc.previewed.NotificationType)
	assert.Equal(t, "Hi", uc.previewed.Subject)
	assert.Equal(t, "pre", uc.previewed.Preheader)
	assert.JSONEq(t, `[{"type":"logo"}]`, string(uc.previewed.Body))
	assert.JSONEq(t, `{"cta_bg_color":"theme:brand"}`, string(uc.previewed.Styling))
	assert.Equal(t, map[string]string{"user_first_name": "Ada"}, uc.previewed.Values)

	var resp struct {
		Data emailPreviewResponse `json:"Data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "S:Hi", resp.Data.Subject)
	assert.Equal(t, "P:pre", resp.Data.Preheader)
	assert.Equal(t, "<p>"+uc.imageID.String()+"</p>", resp.Data.HTML)
}

func TestPreviewEmail_InvalidStylingIs400(t *testing.T) {
	uc := &fakeUseCase{previewErr: notificationModel.ErrInvalidTemplateStyling.Err()}
	w := postPreview(newEngine(uc), `{"NotificationType":"participant.approval_registration.approved","Body":[]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestPreviewEmail_MalformedJSONIs400(t *testing.T) {
	w := postPreview(newEngine(&fakeUseCase{}), `{`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// A half-typed draft reaches the route through the real preview: it must be a
// 400 whose message names the renderer's reason (not a masked 500).
func TestPreviewEmail_UnrenderableDraftIs400WithReason(t *testing.T) {
	uc := &fakeUseCase{realPreview: true}
	w := postPreview(newEngine(uc), `{"NotificationType":"participant.approval_registration.approved","Subject":"Hi {{.user_first_name","Body":[]}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var resp struct {
		Status struct{ Message string } `json:"Status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, strings.HasPrefix(resp.Status.Message, "Template cannot be rendered: "), resp.Status.Message)
	assert.Contains(t, resp.Status.Message, "{{.Variable}}")
}

// The preview must not depend on image requests (the sandboxed iframe sends
// no cookies): the logo arrives inline as a data: URI, never as an /api/ URL.
func TestPreviewEmail_InlinesImagesAsDataURIs(t *testing.T) {
	uc := &fakeUseCase{realPreview: true}
	w := postPreview(newEngine(uc), `{"NotificationType":"participant.approval_registration.approved","Subject":"Hi","Body":[{"type":"logo"}]}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data emailPreviewResponse `json:"Data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp.Data.HTML, `src="data:image/png;base64,`)
	assert.NotContains(t, resp.Data.HTML, "/api/")
}
