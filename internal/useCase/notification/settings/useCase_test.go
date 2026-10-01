package settingsUseCase_test

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/notification/settings"
	"github.com/cybericebox/daemon/internal/useCase/notification/settings"
	"github.com/cybericebox/daemon/pkg/tools"
)

func setupSettings(
	t *testing.T,
) (*gomock.Controller, *postgresMocks.MockQuerier, *settingsUseCase.NotificationSettingsUseCase) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := settingsUseCase.NewNotificationSettingsUseCase(settingsUseCase.Dependencies{Repo: repo})
	return ctrl, repo, uc
}

func TestListGlobalSettings_HappyPath(t *testing.T) {
	ctrl, repo, uc := setupSettings(t)
	defer ctrl.Finish()
	ctx := context.Background()

	expected := []postgres.NotificationSetting{
		{
			NotificationType: "email_confirmation",
			Channel:          "email",
			Enabled:          true,
			UserCanChange:    false,
			UserDefault:      false,
		},
		{
			NotificationType: "team_invite",
			Channel:          "in_app",
			Enabled:          true,
			UserCanChange:    true,
			UserDefault:      true,
		},
	}
	repo.EXPECT().ListNotificationSettings(gomock.Any()).Return(expected, nil)

	got, err := uc.ListGlobalSettings(ctx)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "email_confirmation", got[0].NotificationType)
}

func TestUpsertGlobalSetting_HappyPath(t *testing.T) {
	ctrl, repo, uc := setupSettings(t)
	defer ctrl.Finish()
	ctx := context.Background()

	expected := postgres.NotificationSetting{
		NotificationType: "email_confirmation",
		Channel:          "email",
		Enabled:          true,
		UserCanChange:    false,
		UserDefault:      false,
	}
	repo.EXPECT().
		UpsertNotificationSetting(
			gomock.Any(), postgres.UpsertNotificationSettingParams{
				NotificationType: "email_confirmation",
				Channel:          "email",
				Enabled:          true,
				UserCanChange:    false,
				UserDefault:      false,
			},
		).
		Return(expected, nil)

	got, err := uc.UpsertGlobalSetting(
		ctx, settingsModel.UpsertGlobalInput{
			NotificationType: "email_confirmation",
			Channel:          "email",
			Enabled:          true,
			UserCanChange:    false,
			UserDefault:      false,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "email_confirmation", got.NotificationType)
	assert.True(t, got.Enabled)
}

func TestListUserSettings_HappyPath(t *testing.T) {
	ctrl, repo, uc := setupSettings(t)
	defer ctrl.Finish()
	ctx := context.Background()

	userID := tools.NewUUIDv7()
	expected := []postgres.NotificationUserSetting{
		{UserID: userID, NotificationType: "email_confirmation", Channel: "email", Enabled: false},
	}
	repo.EXPECT().ListUserSettings(gomock.Any(), userID).Return(expected, nil)

	got, err := uc.ListUserSettings(ctx, userID)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, userID, got[0].UserID)
}

func TestUpsertUserSetting_HappyPath(t *testing.T) {
	ctrl, repo, uc := setupSettings(t)
	defer ctrl.Finish()
	ctx := context.Background()

	userID := tools.NewUUIDv7()
	repo.EXPECT().
		UpsertUserSetting(
			gomock.Any(), postgres.UpsertUserSettingParams{
				UserID:           userID,
				NotificationType: "email_confirmation",
				Channel:          "email",
				Enabled:          false,
			},
		).
		Return(nil)

	err := uc.UpsertUserSetting(
		ctx, settingsModel.UpsertUserInput{
			UserID:           userID,
			NotificationType: "email_confirmation",
			Channel:          "email",
			Enabled:          false,
		},
	)
	require.NoError(t, err)
}
