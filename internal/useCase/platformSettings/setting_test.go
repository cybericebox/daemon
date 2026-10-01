package platformSettingsUseCase

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/tools"
)

func setup(t *testing.T) (
	*gomock.Controller,
	*postgresMocks.MockQuerier,
	*PlatformSettingsUseCase,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := NewPlatformSettingsUseCase(Dependencies{Repo: repo})
	return ctrl, repo, uc
}

func sampleRow(key string, requiredPermission string) postgres.AppSetting {
	return postgres.AppSetting{
		ID:                 tools.NewUUIDv7(),
		Key:                key,
		Value:              []byte(`{"a":1}`),
		RequiredPermission: requiredPermission,
	}
}

// TestGet_PublicSettingAccessibleToAll verifies that a setting with an empty
// required_permission is returned to any caller, including one with no role.
func TestGet_PublicSettingAccessibleToAll(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	// no role in context — unauthenticated caller
	ctx := context.Background()
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), "theme").Return(sampleRow("theme", ""), nil)

	got, err := uc.GetPlatformSetting(ctx, "theme")
	require.NoError(t, err)
	assert.Equal(t, "theme", got.Key)
}

// TestGet_PermissionedSettingAllowedBySuperAdmin verifies that a setting requiring
// "platform.settings.read" is returned to super_admin (held via "*").
func TestGet_PermissionedSettingAllowedBySuperAdmin(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleSuperAdmin})
	repo.EXPECT().
		GetPlatformSettingByKey(gomock.Any(), "secret").
		Return(sampleRow("secret", string(rbac.PermPlatformSettingsRead)), nil)

	got, err := uc.GetPlatformSetting(ctx, "secret")
	require.NoError(t, err)
	assert.Equal(t, "secret", got.Key)
}

// TestGet_PermissionedSettingDeniedForAdmin verifies that under the super_admin-only
// platform-settings policy a permissioned setting is hidden from a regular admin.
func TestGet_PermissionedSettingDeniedForAdmin(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleAdmin})
	repo.EXPECT().
		GetPlatformSettingByKey(gomock.Any(), "secret").
		Return(sampleRow("secret", string(rbac.PermPlatformSettingsRead)), nil)

	_, err := uc.GetPlatformSetting(ctx, "secret")
	require.Error(t, err)
	assert.True(t, platformSettingsModel.ErrSettingNotFound.Err().Is(err))
}

// TestGet_DeniedByPermission_ReturnsNotFound verifies that a caller lacking the
// required permission receives ErrSettingNotFound (existence is not leaked).
func TestGet_DeniedByPermission_ReturnsNotFound(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleUser}) // RoleUser holds no permissions
	repo.EXPECT().
		GetPlatformSettingByKey(gomock.Any(), "secret").
		Return(sampleRow("secret", string(rbac.PermPlatformSettingsRead)), nil)

	_, err := uc.GetPlatformSetting(ctx, "secret")
	require.Error(t, err)
	assert.True(t, platformSettingsModel.ErrSettingNotFound.Err().Is(err))
}

// TestList_FiltersPermissionedForRoleUser verifies that ListSettings filters out
// settings requiring "platform.settings.read" for a RoleUser caller, while
// returning publicly-readable (empty required_permission) settings.
func TestList_FiltersPermissionedForRoleUser(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleUser})
	repo.EXPECT().ListPlatformSettings(gomock.Any()).Return([]postgres.AppSetting{
		sampleRow("public-key", ""),
		sampleRow("admin-key", string(rbac.PermPlatformSettingsRead)),
	}, nil)

	got, err := uc.ListPlatformSettings(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "public-key", got[0].Key)
}

// TestList_ReturnsAllForSuperAdmin verifies that super_admin sees both public and
// permissioned settings.
func TestList_ReturnsAllForSuperAdmin(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleSuperAdmin})
	repo.EXPECT().ListPlatformSettings(gomock.Any()).Return([]postgres.AppSetting{
		sampleRow("public-key", ""),
		sampleRow("admin-key", string(rbac.PermPlatformSettingsRead)),
	}, nil)

	got, err := uc.ListPlatformSettings(ctx)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// TestList_PublicSettingAccessibleWithNoRole verifies that a setting with empty
// required_permission is returned even when no role is set in ctx.
func TestList_PublicSettingAccessibleWithNoRole(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := context.Background() // no role
	repo.EXPECT().ListPlatformSettings(gomock.Any()).Return([]postgres.AppSetting{
		sampleRow("public-key", ""),
	}, nil)

	got, err := uc.ListPlatformSettings(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "public-key", got[0].Key)
}

func TestUpsert_AtomicStatement(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := context.Background()

	repo.EXPECT().
		UpsertPlatformSetting(gomock.Any(), gomock.AssignableToTypeOf(postgres.UpsertPlatformSettingParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.UpsertPlatformSettingParams) (postgres.AppSetting, error) {
			if arg.ID == uuid.Nil {
				t.Fatal("id must come from the domain factory")
			}
			if arg.CreatedAt.IsZero() || arg.UpdatedAt.IsZero() {
				t.Fatal("timestamps must come from the domain factory")
			}
			if arg.Key != "k" || string(arg.Value) != `{"a":1}` {
				t.Fatalf("unexpected upsert args: %+v", arg)
			}
			return sampleRow("k", ""), nil
		})

	got, err := uc.UpsertPlatformSetting(
		ctx,
		UpsertInput{Key: "k", Value: json.RawMessage(`{"a":1}`), RequiredPermission: ""},
	)
	require.NoError(t, err)
	assert.Equal(t, "k", got.Key)
}

func TestUpsert_RepoError(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()

	repo.EXPECT().UpsertPlatformSetting(gomock.Any(), gomock.Any()).
		Return(postgres.AppSetting{}, pgx.ErrTxClosed)

	if _, err := uc.UpsertPlatformSetting(
		context.Background(),
		UpsertInput{Key: "k", Value: json.RawMessage(`{}`)},
	); err == nil {
		t.Fatal("repo failure must surface")
	}
}
