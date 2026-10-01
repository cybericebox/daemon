package rbac_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/cybericebox/daemon/internal/model/rbac"
)

func TestCurrentUserSessionContext_RoundTrip(t *testing.T) {
	claims := rbac.Claims{
		UserID:    uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()),
		Role:      rbac.RoleAdmin,
	}
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), claims)

	got, ok := rbac.CurrentUserSessionFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, claims, got)
}

func TestCurrentUserSessionContext_Absent(t *testing.T) {
	if _, ok := rbac.CurrentUserSessionFromContext(context.Background()); ok {
		t.Fatal("empty context must report no session")
	}
}

func TestHasPermissionInContext_Absent(t *testing.T) {
	if rbac.HasPermissionInContext(context.Background(), rbac.PermUsersRead) {
		t.Fatal("no session in context must fail-closed")
	}
}

func TestHasPermissionInContext_RoleCovered(t *testing.T) {
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{
		UserID:    uuid.Must(uuid.NewV7()),
		SessionID: uuid.Must(uuid.NewV7()),
		Role:      rbac.RoleAdminViewer,
	})
	assert.True(t, rbac.HasPermissionInContext(ctx, rbac.PermUsersRead))
	assert.False(t, rbac.HasPermissionInContext(ctx, rbac.PermUsersDelete))
}

func TestAnalyticsPermissionsAreSuperAdminOnly(t *testing.T) {
	for _, perm := range []rbac.Permission{rbac.PermAnalyticsRead, rbac.PermAnalyticsUsersRead} {
		assert.True(t, rbac.RoleSuperAdmin.HasPermission(perm), perm)
		for _, role := range []rbac.Role{rbac.RoleAdmin, rbac.RoleAdminViewer, rbac.RoleUser, rbac.RolePublic} {
			assert.False(t, role.HasPermission(perm), "%s must not hold %s", role, perm)
		}
	}
}
