package dispatcherUseCase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	payloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
)

type recordingQueue struct {
	worker.IEnqueuer
	queued []jobsModel.NotifyArgs
	err    error
}

func (q *recordingQueue) Enqueue(_ context.Context, args interface{ Kind() string }) error {
	if q.err != nil {
		return q.err
	}
	q.queued = append(q.queued, args.(jobsModel.NotifyArgs))
	return nil
}

// sealedPayload stores a notification payload through Notify and returns the stored row with its dispatch id.
func sealedPayload(t *testing.T, repo *postgresMocks.MockQuerier, user uuid.UUID) (postgres.CreateTemporalCodeParams, uuid.UUID) {
	t.Helper()
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{
		Repo: repo, Enqueuer: &transactionalQueue{IEnqueuer: worker.NewEnqueuer()}, Cipher: testCipher(t),
		UnitOfWorker: dispatcherUnit{repo, &transactionHandle{}},
	})
	var stored postgres.CreateTemporalCodeParams
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, nil)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
		stored = arg
		return postgres.TemporalCode{}, nil
	})
	require.NoError(t, uc.Notify(context.Background(), user, payloads.PasswordResetPayload{ResetURL: "https://x/reset", Name: "Ira"}))
	id, err := uuid.FromString(stored.Code[len("notify-payload:"):])
	require.NoError(t, err)
	return stored, id
}

func resendHarness(t *testing.T) (*postgresMocks.MockQuerier, *dispatcherUseCase.NotificationDispatcher, *recordingQueue, postgres.CreateTemporalCodeParams, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	user := tools.NewUUIDv7()
	stored, dispatchID := sealedPayload(t, repo, user)
	queue := &recordingQueue{IEnqueuer: worker.NewEnqueuer()}
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: queue, Cipher: testCipher(t)})
	return repo, uc, queue, stored, dispatchID, user
}

func dispatchRow(id, user uuid.UUID) postgres.GetDispatchRow {
	return postgres.GetDispatchRow{ID: id, NotificationType: "password_reset", RecipientUserID: user, Status: "started"}
}

// Only the failed channels go out again; the dispatch is marked pending before the job is queued.
func TestResendDispatch_QueuesTheFailedChannelsOnly(t *testing.T) {
	repo, uc, queue, stored, id, user := resendHarness(t)
	repo.EXPECT().GetDispatch(gomock.Any(), id).Return(dispatchRow(id, user), nil)
	repo.EXPECT().ListDispatchTargets(gomock.Any(), id).Return([]postgres.NotificationDispatchTarget{
		{DispatchID: id, Channel: "email", Status: "error", Error: "535 auth"},
		{DispatchID: id, Channel: "in_app", Status: "done"},
	}, nil)
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), stored.Code).Return(postgres.TemporalCode{ID: stored.ID, Code: stored.Code, Type: stored.Type, Data: stored.Data}, nil)
	repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "pending"}).Return(nil)

	require.NoError(t, uc.ResendDispatch(context.Background(), id))
	require.Len(t, queue.queued, 1)
	require.Equal(t, id, queue.queued[0].DispatchID)
	require.Equal(t, user, queue.queued[0].UserID)
	require.Equal(t, []string{"email"}, queue.queued[0].OverrideChannels, "the delivered in-app copy is not sent twice")
}

func TestResendDispatch_RefusesWhatCannotBeResent(t *testing.T) {
	t.Run("nothing failed", func(t *testing.T) {
		repo, uc, queue, _, id, user := resendHarness(t)
		repo.EXPECT().GetDispatch(gomock.Any(), id).Return(dispatchRow(id, user), nil)
		repo.EXPECT().ListDispatchTargets(gomock.Any(), id).Return([]postgres.NotificationDispatchTarget{{DispatchID: id, Channel: "email", Status: "done"}}, nil)
		err := uc.ResendDispatch(context.Background(), id)
		require.True(t, notificationModel.ErrDispatchNotFailed.Err().Is(err), "%v", err)
		require.Empty(t, queue.queued)
	})
	t.Run("content expired", func(t *testing.T) {
		repo, uc, queue, stored, id, user := resendHarness(t)
		repo.EXPECT().GetDispatch(gomock.Any(), id).Return(dispatchRow(id, user), nil)
		repo.EXPECT().ListDispatchTargets(gomock.Any(), id).Return([]postgres.NotificationDispatchTarget{{DispatchID: id, Channel: "email", Status: "error"}}, nil)
		repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), stored.Code).Return(postgres.TemporalCode{}, pgx.ErrNoRows)
		err := uc.ResendDispatch(context.Background(), id)
		require.True(t, notificationModel.ErrDispatchPayloadGone.Err().Is(err), "%v", err)
		require.Empty(t, queue.queued)
	})
	t.Run("unknown dispatch", func(t *testing.T) {
		repo, uc, _, _, id, _ := resendHarness(t)
		repo.EXPECT().GetDispatch(gomock.Any(), id).Return(postgres.GetDispatchRow{}, pgx.ErrNoRows)
		err := uc.ResendDispatch(context.Background(), id)
		require.True(t, notificationModel.ErrDispatchNotFound.Err().Is(err), "%v", err)
	})
	t.Run("queue down restores the status", func(t *testing.T) {
		repo, uc, queue, stored, id, user := resendHarness(t)
		queue.err = errors.New("queue unavailable")
		repo.EXPECT().GetDispatch(gomock.Any(), id).Return(dispatchRow(id, user), nil)
		repo.EXPECT().ListDispatchTargets(gomock.Any(), id).Return([]postgres.NotificationDispatchTarget{{DispatchID: id, Channel: "email", Status: "error"}}, nil)
		repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), stored.Code).Return(postgres.TemporalCode{ID: stored.ID, Code: stored.Code, Type: stored.Type, Data: stored.Data}, nil)
		gomock.InOrder(
			repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "pending"}).Return(nil),
			repo.EXPECT().SetDispatchStatus(gomock.Any(), postgres.SetDispatchStatusParams{ID: id, Status: "started"}).Return(nil),
		)
		require.Error(t, uc.ResendDispatch(context.Background(), id))
	})
}
