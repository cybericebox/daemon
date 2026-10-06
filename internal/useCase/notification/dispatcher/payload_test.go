package dispatcherUseCase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	payloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
)

func testCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	c, err := secret.New(strings.Repeat("ab", 32))
	require.NoError(t, err)
	return c
}

func TestNotify_SealsTheVariablesAndReadsThemBack(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: worker.NewEnqueuer(), Cipher: testCipher(t)})
	userID := tools.NewUUIDv7()

	var stored postgres.CreateTemporalCodeParams
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, nil)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
		stored = arg
		return postgres.TemporalCode{}, nil
	})

	err := uc.Notify(context.Background(), userID, payloads.PasswordResetPayload{ResetURL: "https://x/reset?token=SECRET", Name: "Ira Koval"})
	require.NoError(t, err)

	require.Equal(t, temporalCodeModel.NotificationPayloadCodeType, stored.Type)
	require.NotContains(t, string(stored.Data), "SECRET")
	require.NotContains(t, string(stored.Data), "Ira")
	require.Contains(t, string(stored.Data), userID.String(), "the row names its user so an account deletion removes it")

	dispatchID := strings.TrimPrefix(stored.Code, "notify-payload:")
	require.NotEqual(t, dispatchID, stored.Code)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), stored.Code).Return(postgres.TemporalCode{ID: stored.ID, Code: stored.Code, Type: stored.Type, Data: stored.Data}, nil)
	id, err := uuid.FromString(dispatchID)
	require.NoError(t, err)
	p, err := uc.LoadNotificationPayload(context.Background(), id)
	require.NoError(t, err)
	require.Contains(t, string(p.Vars), "SECRET")
}

func TestNotify_WithoutACipherQueuesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: worker.NewEnqueuer()})
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, nil)

	err := uc.Notify(context.Background(), tools.NewUUIDv7(), payloads.PasswordResetPayload{Name: "Ira"})
	require.Error(t, err)
}
