package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/rbac"
)

// L22: a use case that receives the *gin.Context as its context.Context must
// find the caller's identity there.
func TestRouterExposesClaimsThroughTheGinContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newRouter()
	uid := uuid.Must(uuid.NewV7())
	var got rbac.Claims
	var found bool
	router.GET("/x", func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uid, Role: rbac.RoleAdmin}))
		got, found = rbac.CurrentUserSessionFromContext(c) // c, not c.Request.Context()
		c.Status(http.StatusOK)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !found || got.UserID != uid || got.Role != rbac.RoleAdmin {
		t.Fatalf("claims not visible through *gin.Context: found=%v %+v", found, got)
	}
}
