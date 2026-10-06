package event_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// denyAllProt rejects every gated route, so a 200 proves the route is public.
type denyAllProt struct{}

func (denyAllProt) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.AbortWithStatus(http.StatusUnauthorized) }
}

func newEventRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New() // no identity injected
	// nil use case: the public stub must not touch it.
	eventHandler.NewEventAPIHandler(nil, denyAllProt{}).Init(r.Group("api"))
	return r
}

func TestUpcoming_PublicReturnsNullData(t *testing.T) {
	r := newEventRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/events/upcoming", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data, ok := env["Data"]; !ok || string(data) != "null" {
		t.Fatalf("Data = %s (present=%v), want null", data, ok)
	}
	if _, ok := env["Status"]; !ok {
		t.Fatalf("missing Status envelope: %s", w.Body.String())
	}
}

// The static "upcoming" segment must not open up the gated /events/:id route.
func TestUpcoming_DoesNotUngateEventByID(t *testing.T) {
	r := newEventRouter()
	const eventURL = "/api/events/0190a0b2-0000-7000-8000-000000000000"
	req := httptest.NewRequest(http.MethodGet, eventURL, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 from the gate, got %d", w.Code)
	}
}
