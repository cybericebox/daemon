package settingsUseCase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	settingsUseCase "github.com/cybericebox/daemon/internal/useCase/notification/settings"
)

func TestListSignalDefaults_ReturnsOnlyEventScopedTypes(t *testing.T) {
	ctrl, repo, uc := setupSettings(t)
	defer ctrl.Finish()

	repo.EXPECT().ListPlatformSignalNotificationDefaults(gomock.Any()).Return([]postgres.PlatformSignalNotificationDefault{
		{SignalType: "event.manager.assigned", Channel: "in_app", Enabled: true, Audience: []byte(`{"kind":"signal_subject"}`)},
		{SignalType: "participant.approval_registration.approved", Channel: "email", Enabled: false, Audience: []byte(`{"kind":"signal_subject"}`)},
	}, nil)

	got, err := uc.ListSignalDefaults(context.Background())
	require.NoError(t, err)
	require.Equal(t, []settingsUseCase.SignalDefault{
		{SignalType: "participant.approval_registration.approved", Channel: "email", Enabled: false, Audience: json.RawMessage(`{"kind":"signal_subject"}`)},
	}, got)
}

func TestUpsertSignalDefault_WritesValidDefault(t *testing.T) {
	ctrl, repo, uc := setupSettings(t)
	defer ctrl.Finish()

	params := postgres.UpsertPlatformSignalNotificationDefaultParams{
		SignalType: "participant.approval_registration.approved", Channel: "in_app", Enabled: true, Audience: []byte(`{"kind":"all_participants"}`),
	}
	repo.EXPECT().UpsertPlatformSignalNotificationDefault(gomock.Any(), params).Return(postgres.PlatformSignalNotificationDefault{
		SignalType: params.SignalType, Channel: params.Channel, Enabled: params.Enabled, Audience: params.Audience,
	}, nil)

	got, err := uc.UpsertSignalDefault(context.Background(), settingsUseCase.SignalDefault{
		SignalType: "participant.approval_registration.approved", Channel: "in_app", Enabled: true, Audience: json.RawMessage(`{"kind":"all_participants"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "participant.approval_registration.approved", got.SignalType)
	require.True(t, got.Enabled)
}

func TestUpsertSignalDefault_RejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   settingsUseCase.SignalDefault
		want error
	}{
		{
			name: "platform-only type",
			in:   settingsUseCase.SignalDefault{SignalType: "event.manager.assigned", Channel: "in_app", Audience: json.RawMessage(`{"kind":"signal_subject"}`)},
			want: notificationModel.ErrSignalDefaultTypeNotEventScoped.Err(),
		},
		{
			name: "unsupported channel",
			in:   settingsUseCase.SignalDefault{SignalType: "participant.approval_registration.approved", Channel: "sms", Audience: json.RawMessage(`{"kind":"signal_subject"}`)},
			want: notificationModel.ErrSignalDefaultChannelUnsupported.Err(),
		},
		{
			name: "malformed audience",
			in:   settingsUseCase.SignalDefault{SignalType: "participant.approval_registration.approved", Channel: "email", Audience: json.RawMessage(`"nope"`)},
			want: notificationModel.ErrSignalDefaultInvalidAudience.Err(),
		},
		{
			name: "selected users without users",
			in:   settingsUseCase.SignalDefault{SignalType: "participant.approval_registration.approved", Channel: "email", Audience: json.RawMessage(`{"kind":"selected_users"}`)},
			want: notificationModel.ErrSignalDefaultInvalidAudience.Err(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl, _, uc := setupSettings(t)
			defer ctrl.Finish()
			_, err := uc.UpsertSignalDefault(context.Background(), tc.in)
			require.True(t, errors.Is(err, tc.want), "got %v", err)
		})
	}
}
