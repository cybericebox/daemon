package event

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type resultsEndModeUseCase struct {
	IUseCase
	settings eventConfigModel.ResultsSettings
	current  eventUseCase.ResultsSettingsView
}

func (u *resultsEndModeUseCase) GetResultsSettings(context.Context, uuid.UUID) (eventUseCase.ResultsSettingsView, error) {
	return u.current, nil
}

func (u *resultsEndModeUseCase) UpdateResultsSettings(_ context.Context, _ uuid.UUID, _ eventConfigModel.Visibility, settings eventConfigModel.ResultsSettings, _ uuid.UUID) (eventUseCase.ResultsSettingsView, error) {
	u.settings = settings
	var view eventUseCase.ResultsSettingsView
	err := json.Unmarshal([]byte(`{"FreezeEndMode":"manual","FreezeBannerText":"Підсумки згодом.","FreezeBannerLevel":"critical"}`), &view)
	return view, err
}

func TestResultsSettingsHandlerAcceptsManualFreezeEndMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	u := &resultsEndModeUseCase{}
	h := NewEventAPIHandler(u, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Params = gin.Params{{Key: "id", Value: uuid.Must(uuid.NewV7()).String()}}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"ScoreboardVisibility":2,"FreezeEnabled":true,"FreezeMinutes":30,"FreezeEndMode":"manual","FreezeBannerText":"Підсумки згодом.","FreezeBannerLevel":"critical","LiveFreeze":true,"ChartEnabled":true,"ChartTeams":5}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7())}))
	h.updateResultsSettings(ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body, err := json.Marshal(u.settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"FreezeEndMode":"manual"`) || !strings.Contains(w.Body.String(), `"FreezeEndMode":"manual"`) {
		t.Fatalf("mode lost at settings boundary: input=%s output=%s", body, w.Body.String())
	}
	for _, field := range []string{`"FreezeBannerText":"Підсумки згодом."`, `"FreezeBannerLevel":"critical"`} {
		if !strings.Contains(string(body), field) || !strings.Contains(w.Body.String(), field) {
			t.Errorf("banner lost at settings boundary: input=%s output=%s", body, w.Body.String())
		}
	}
}

func TestResultsSettingsResponseDefaultsLegacyFreezeEndMode(t *testing.T) {
	body, err := json.Marshal(toResultsSettingsResponse(eventUseCase.ResultsSettingsView{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"FreezeEndMode":"at_finish"`) {
		t.Fatalf("legacy mode is not canonical: %s", body)
	}
	if !strings.Contains(string(body), `"FreezeBannerText":""`) || !strings.Contains(string(body), `"FreezeBannerLevel":"warning"`) {
		t.Errorf("legacy banner defaults lost: %s", body)
	}
}

func TestResultsSettingsRejectsUnknownFreezeOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"FreezeEndMode":"forever"}`, `{"FreezeBannerLevel":"danger"}`} {
		h := NewEventAPIHandler(&resultsEndModeUseCase{}, nil)
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Params = gin.Params{{Key: "id", Value: uuid.Must(uuid.NewV7()).String()}}
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx.Request = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7())}))
		h.updateResultsSettings(ctx)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid settings accepted: %s -> %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestLegacyResultsSettingsPutPreservesConfiguredFreezeFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, textField := range []string{"", `,"FreezeBannerText":""`} {
		u := &resultsEndModeUseCase{current: eventUseCase.ResultsSettingsView{
			FreezeEndMode: eventConfigModel.FreezeEndManual, FreezeBannerText: "Підсумки згодом.", FreezeBannerLevel: "critical", LiveFreeze: false,
		}}
		h := NewEventAPIHandler(u, nil)
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Params = gin.Params{{Key: "id", Value: uuid.Must(uuid.NewV7()).String()}}
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"ScoreboardVisibility":2,"FreezeEnabled":true,"FreezeMinutes":45,"ChartEnabled":true,"ChartTeams":5`+textField+`}`))
		req.Header.Set("Content-Type", "application/json")
		ctx.Request = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7())}))
		h.updateResultsSettings(ctx)
		if w.Code != http.StatusOK {
			t.Fatalf("legacy update=%d %s", w.Code, w.Body.String())
		}
		wantText := "Підсумки згодом."
		if textField != "" {
			wantText = ""
		}
		if u.settings.FreezeEndMode != eventConfigModel.FreezeEndManual || u.settings.FreezeBannerText != wantText || u.settings.FreezeBannerLevel != "critical" || u.settings.LiveFreeze {
			t.Errorf("legacy update reset settings (explicit clear=%t): %+v", textField != "", u.settings)
		}
	}
}
