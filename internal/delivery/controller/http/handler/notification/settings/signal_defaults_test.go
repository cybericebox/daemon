package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

func (f *fakeUseCase) ListSignalDefaults(context.Context) ([]settingsModel.SignalDefault, error) {
	return []settingsModel.SignalDefault{{
		SignalType: "participant.enrolled", Channel: "email", Enabled: false, Audience: json.RawMessage(`{"kind":"signal_subject"}`),
	}}, nil
}

func (f *fakeUseCase) UpsertSignalDefault(_ context.Context, in settingsModel.SignalDefault) (settingsModel.SignalDefault, error) {
	f.gotSignalDefault = in
	return in, nil
}

// recordingProt passes every request and records, in registration order, the
// permission each route was gated with.
type recordingProt struct{ perms []rbac.Permission }

func (p *recordingProt) RequirePermission(perm rbac.Permission) gin.HandlerFunc {
	p.perms = append(p.perms, perm)
	return func(c *gin.Context) { c.Next() }
}

func signalDefaultsRequest(t *testing.T, engine *gin.Engine, method, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/notifications/signal-defaults", strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope), w.Body.String())
	return w.Code, envelope
}

func TestSignalDefaultsHTTPContract(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{}
	engine := newEngine(uc, &uid)

	code, listed := signalDefaultsRequest(t, engine, http.MethodGet, "")
	require.Equal(t, http.StatusOK, code)
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(listed["Data"], &items))
	require.Len(t, items, 1)
	require.JSONEq(t, `"participant.enrolled"`, string(items[0]["SignalType"]))
	require.JSONEq(t, `"email"`, string(items[0]["Channel"]))
	require.JSONEq(t, `false`, string(items[0]["Enabled"]))
	require.JSONEq(t, `{"kind":"signal_subject"}`, string(items[0]["Audience"]))

	code, saved := signalDefaultsRequest(t, engine, http.MethodPut,
		`{"SignalType":"participant.enrolled","Channel":"in_app","Enabled":true,"Audience":{"kind":"all_participants"}}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, settingsModel.SignalDefault{
		SignalType: "participant.enrolled", Channel: "in_app", Enabled: true, Audience: json.RawMessage(`{"kind":"all_participants"}`),
	}, uc.gotSignalDefault)
	require.Contains(t, string(saved["Data"]), `"Enabled":true`)
}

func TestSignalDefaultsRoutesUseSettingsPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prot := &recordingProt{}
	engine := gin.New()
	NewSettingsAPIHandler(&fakeUseCase{}, prot).Init(engine.Group("api/notifications"))
	routes := map[string]bool{}
	for _, r := range engine.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	// Gates are recorded in Init order; the signal-defaults pair is registered
	// after the four settings routes.
	require.Equal(t, []rbac.Permission{
		rbac.PermNotificationsSettingsRead, rbac.PermNotificationsSettingsWrite,
		rbac.PermNotificationsSelf, rbac.PermNotificationsSelf,
		rbac.PermNotificationsSettingsRead, rbac.PermNotificationsSettingsWrite,
	}, prot.perms)
	require.True(t, routes["GET /api/notifications/signal-defaults"])
	require.True(t, routes["PUT /api/notifications/signal-defaults"])
}

func TestUpsertSignalDefault_DeniedByProt_403(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	engine := newEngineWithProt(&fakeUseCase{}, &uid, denyProt{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/notifications/signal-defaults",
		strings.NewReader(`{"SignalType":"participant.enrolled","Channel":"email","Enabled":true,"Audience":{"kind":"signal_subject"}}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}
