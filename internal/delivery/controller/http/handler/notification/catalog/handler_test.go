package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// fakeProt satisfies IProtection; RequirePermission is a no-op pass-through.
type fakeProt struct{}

func (fakeProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

func newEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(response.WithErrorHandler)
	NewCatalogAPIHandler(fakeProt{}).Init(engine.Group("api/notifications"))
	return engine
}

func TestListTypes_ReturnsRegistered(t *testing.T) {
	engine := newEngine()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/types", nil)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data []struct {
			Type      string `json:"Type"`
			Variables []struct {
				Name string `json:"Name"`
			} `json:"Variables"`
		} `json:"Data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body.Data)
}

func TestGetType_ReturnsOne(t *testing.T) {
	engine := newEngine()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/types/flag_accepted", nil)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data struct {
			Type      string   `json:"Type"`
			Channels  []string `json:"Channels"`
			Variables []struct {
				Name string `json:"Name"`
			} `json:"Variables"`
		} `json:"Data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "flag_accepted", body.Data.Type)
	assert.NotEmpty(t, body.Data.Channels)
}

func TestGetType_Unknown_404(t *testing.T) {
	engine := newEngine()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/types/does_not_exist", nil)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
