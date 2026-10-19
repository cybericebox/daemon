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
	retention, testRuns := typedDefaults()
	uc := NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retention, TestRunDefaults: testRuns})
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

// An empty required_permission means an administrator, never anonymous: per role, only the super_admin (the one role
// holding platform.settings.read) reads it; everyone else gets ErrSettingNotFound (existence is not leaked).
func TestGet_EmptyPermissionIsAdminOnly(t *testing.T) {
	cases := []struct {
		name    string
		ctx     context.Context
		allowed bool
	}{
		{"anonymous", context.Background(), false},
		{"public", rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RolePublic}), false},
		{"user", rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleUser}), false},
		{"admin_viewer", rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleAdminViewer}), false},
		{"admin", rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleAdmin}), false},
		{"super_admin", rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleSuperAdmin}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctrl, repo, uc := setup(t)
			defer ctrl.Finish()
			repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), "theme").Return(sampleRow("theme", ""), nil)
			got, err := uc.GetPlatformSettingValue(c.ctx, "theme")
			if c.allowed {
				require.NoError(t, err)
				assert.JSONEq(t, `{"a":1}`, string(got))
				return
			}
			assert.True(t, platformSettingsModel.ErrSettingNotFound.Err().Is(err), "got %v", err)
		})
	}
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

// TestList_HidesEverythingFromNonAdmins: with no public level, a user sees no setting at all, empty
// required_permission included.
func TestList_HidesEverythingFromNonAdmins(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleUser})
	repo.EXPECT().ListPlatformSettings(gomock.Any()).Return([]postgres.AppSetting{
		sampleRow("default-key", ""),
		sampleRow("admin-key", string(rbac.PermPlatformSettingsRead)),
	}, nil)

	got, err := uc.ListPlatformSettings(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
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

// TestList_NothingForAnonymous: an anonymous caller lists nothing.
func TestList_NothingForAnonymous(t *testing.T) {
	ctrl, repo, uc := setup(t)
	defer ctrl.Finish()
	repo.EXPECT().ListPlatformSettings(gomock.Any()).Return([]postgres.AppSetting{sampleRow("default-key", "")}, nil)

	got, err := uc.ListPlatformSettings(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// A setting's access is one of the known levels; a free string is refused before anything is stored.
func TestUpsert_RefusesUnknownAccessLevel(t *testing.T) {
	_, _, uc := setup(t)
	for _, permission := range []string{"public", "platform.settings.read.value", "users.read", "*"} {
		_, err := uc.UpsertPlatformSetting(context.Background(), UpsertInput{Key: "k", Value: json.RawMessage(`{}`), RequiredPermission: permission})
		assert.True(t, platformSettingsModel.ErrSettingAccessInvalid.Err().Is(err), "%q: got %v", permission, err)
	}
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

func TestGenericSettingsCannotBypassTypedPreviewPublicationValidation(t *testing.T) {
	_, _, uc := setup(t)
	_, err := uc.UpsertPlatformSetting(context.Background(), UpsertInput{Key: "participant_preview_exercises", Value: json.RawMessage(`[{"ExerciseVersionID":"private"}]`)})
	require.Error(t, err)
}
