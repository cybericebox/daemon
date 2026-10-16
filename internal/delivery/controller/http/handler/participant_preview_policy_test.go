package handler

import (
	"github.com/cybericebox/daemon/internal/model/rbac"
	"strings"
	"testing"
)

func TestParticipantPreviewRoutesKeepEventAndPlatformPermissionsSeparate(t *testing.T) {
	count := 0
	global := 0
	for _, route := range buildRoutePolicy(t) {
		if strings.Contains(route.path, "/manage/participant-preview") {
			count++
			if len(route.gates) != 1 || route.gates[0] != rbac.PermSelf {
				t.Errorf("event preview broad permission: %+v", route)
			}
			if !hasLayer(route, "requireManage", "requirePreviewCleanup") {
				t.Errorf("missing event manager gate: %+v", route)
			}
		}
		if strings.HasPrefix(route.path, "/api/settings/participant-preview") {
			global++
			expected := rbac.PermPlatformSettingsRead
			if route.method == "PUT" {
				expected = rbac.PermPlatformSettingsWrite
			}
			if len(route.gates) != 1 || route.gates[0] != expected {
				t.Errorf("platform preview gate: %+v", route)
			}
		}
	}
	if global != 3 {
		t.Fatalf("global routes %d want 3", global)
	}
	if count != 14 {
		t.Fatalf("preview event routes %d want 14", count)
	}
}
