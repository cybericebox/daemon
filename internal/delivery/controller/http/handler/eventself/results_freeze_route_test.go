package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type freezeRouteUseCase struct {
	fakeUseCase
	read func(context.Context, uuid.UUID, eventUseCase.ResultsAccess) (eventUseCase.ResultsFreezeView, error)
}

func (u freezeRouteUseCase) GetResultsFreeze(ctx context.Context, id uuid.UUID, access eventUseCase.ResultsAccess) (eventUseCase.ResultsFreezeView, error) {
	return u.read(ctx, id, access)
}

func TestResultsFreezeRouteIsStateOnlyPersonalizedAndRevalidated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	applied, denied := true, false
	h := NewEventSelfAPIHandler(freezeRouteUseCase{read: func(_ context.Context, id uuid.UUID, access eventUseCase.ResultsAccess) (eventUseCase.ResultsFreezeView, error) {
		if id != eventID {
			t.Fatalf("wrong event %s", id)
		}
		if access.UserID != nil && (*access.UserID != userID || access.Role != rbac.RoleUser) {
			t.Fatalf("wrong viewer %+v", access)
		}
		if denied {
			return eventUseCase.ResultsFreezeView{}, eventConfigModel.ErrResultsHidden.Err()
		}
		return eventUseCase.ResultsFreezeView{Enabled: true, Active: true, Applied: applied, BannerText: "Підсумки згодом.", BannerLevel: "critical"}, nil
	}}, allowAllProtection{})
	router := gin.New()
	router.Use(response.WithErrorHandler)
	h.Init(router.Group("/api"), func(ctx *gin.Context) { ctx.Next() })
	get := func(tag string, signedIn bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/results/freeze", nil)
		if signedIn {
			req = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: userID, Role: rbac.RoleUser}))
		}
		if tag != "" {
			req.Header.Set("If-None-Match", tag)
		}
		router.ServeHTTP(w, req)
		return w
	}
	first := get("", false)
	if first.Code != http.StatusOK {
		t.Fatalf("freeze route=%d %s", first.Code, first.Body.String())
	}
	var envelope struct{ Data map[string]json.RawMessage }
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 8 || string(envelope.Data["Applied"]) != "true" || string(envelope.Data["BannerText"]) != `"Підсумки згодом."` {
		t.Fatalf("state-only response lost: %s", first.Body.String())
	}
	tag := first.Header().Get("ETag")
	if tag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("cache headers: %v", first.Header())
	}
	if same := get(tag, false); same.Code != http.StatusNotModified || same.Body.Len() != 0 {
		t.Fatalf("unchanged state=%d %s", same.Code, same.Body.String())
	}
	applied = false
	personalized := get(tag, true)
	if personalized.Code != http.StatusOK || personalized.Header().Get("ETag") == tag {
		t.Fatalf("viewer state did not revalidate: %d %s", personalized.Code, personalized.Body.String())
	}
	if err := json.Unmarshal(personalized.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.Data["BannerText"]) != `""` || string(envelope.Data["BannerLevel"]) != `"warning"` {
		t.Fatalf("unapplied authored content leaked: %s", personalized.Body.String())
	}
	denied = true
	if forbidden := get(tag, false); forbidden.Code != http.StatusForbidden || forbidden.Header().Get("ETag") != "" {
		t.Fatalf("gate bypassed by validator: %d %s", forbidden.Code, forbidden.Body.String())
	}
}
