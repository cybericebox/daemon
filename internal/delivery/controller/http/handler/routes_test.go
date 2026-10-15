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
	NewAPIHandler(nil, noopProtector{}, config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}}).Init(router)
	if len(router.Routes()) == 0 {
		t.Fatal("no routes registered")
	}
}
