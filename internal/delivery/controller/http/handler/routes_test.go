package handler

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type noopProtector struct{}

func (noopProtector) Authenticate(*gin.Context, string)                 {}
func (noopProtector) DeAuthenticate(*gin.Context)                       {}
func (noopProtector) IssueClientToken(*gin.Context)                     {}
func (noopProtector) RequireCaptcha(string) gin.HandlerFunc             { return func(*gin.Context) {} }
func (noopProtector) RequirePermission(rbac.Permission) gin.HandlerFunc { return func(*gin.Context) {} }

// Every handler mounts into one router at startup; a path registered twice
// panics there (gin), so the daemon would not start. Build the whole tree.
func TestInitRegistersEveryRouteOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("route registration panicked: %v", r)
		}
	}()
	router := gin.New()
	NewAPIHandler(nil, noopProtector{}, config.AuthConfig{Hosts: config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}}).Init(router)
	if len(router.Routes()) == 0 {
		t.Fatal("no routes registered")
	}
}
