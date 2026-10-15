package dispatcherUseCase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	payloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
)

type transactionHandle struct {
	pgx.Tx
	saved, restored bool
}

func (t *transactionHandle) Save() error    { t.saved = true; return nil }
func (t *transactionHandle) Restore() error { t.restored = true; return nil }

type dispatcherUnit struct {
	repo dispatcherUseCase.Repository
	tx   *transactionHandle
}

func (u dispatcherUnit) UnitOfWork(ctx context.Context) (context.Context, dispatcherUseCase.Repository, postgres.UoW, error) {
	return postgres.ContextWithTransaction(ctx, u.tx), u.repo, u.tx, nil
}

type transactionalQueue struct {
	worker.IEnqueuer
	tx  pgx.Tx
	err error
}

func (q *transactionalQueue) EnqueueTx(_ context.Context, tx pgx.Tx, _ river.JobArgs) error {
	q.tx = tx
	return q.err
}

func TestNotify_DispatchPayloadAndJobShareCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			tx := &transactionHandle{}
			queue := &transactionalQueue{IEnqueuer: worker.NewEnqueuer()}
			if fail {
				queue.err = errors.New("queue unavailable")
			}
			uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: queue, Cipher: testCipher(t), UnitOfWorker: dispatcherUnit{repo, tx}})
			repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, nil)
			repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil)
			err := uc.Notify(context.Background(), tools.NewUUIDv7(), payloads.PasswordResetPayload{Name: "Ira"})
			require.Same(t, tx, queue.tx)
			require.Equal(t, !fail, tx.saved)
			require.True(t, tx.restored)
			if fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type borrowedTransaction struct {
	pgx.Tx
	err     error
	queried bool
}
type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }
func (t *borrowedTransaction) QueryRow(context.Context, string, ...any) pgx.Row {
	t.queried = true
	return failedRow{t.err}
}
func TestNotify_ReusesCallerTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	failure := errors.New("caller transaction unavailable")
	outer := &borrowedTransaction{err: failure}
	inner := &transactionHandle{}
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: &transactionalQueue{IEnqueuer: worker.NewEnqueuer()}, Cipher: testCipher(t), UnitOfWorker: dispatcherUnit{repo, inner}})
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, failure).AnyTimes()
	err := uc.Notify(postgres.ContextWithTransaction(context.Background(), outer), tools.NewUUIDv7(), payloads.PasswordResetPayload{Name: "Ira"})
	require.Error(t, err)
	require.True(t, outer.queried, "the dispatch must use the caller's transaction")
	require.False(t, inner.restored, "must not open or close an independent transaction")
}

func TestNotifyStableProducerIDDoesNotQueueExistingDispatchAgain(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	tx := &transactionHandle{}
	queue := &transactionalQueue{IEnqueuer: worker.NewEnqueuer()}
	uc := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: repo, Enqueuer: queue, Cipher: testCipher(t), UnitOfWorker: dispatcherUnit{repo, tx}})
	id := tools.NewUUIDv7()
	repo.EXPECT().CreateDispatchIfAbsent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateDispatchIfAbsentParams) (int64, error) {
		require.Equal(t, id, p.ID)
		return 0, nil
	})
	require.NoError(t, uc.Notify(context.Background(), tools.NewUUIDv7(), payloads.PasswordResetPayload{}, dispatchModel.WithDispatchID(id)))
	require.Nil(t, queue.tx, "no second job or payload may be created")
}
