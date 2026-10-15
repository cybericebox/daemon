package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/worker"
)

type authTransaction struct {
	pgx.Tx
	committed, restored bool
}

func (t *authTransaction) Save() error    { t.committed = true; return nil }
func (t *authTransaction) Restore() error { t.restored = true; return nil }

type authUnit struct {
	repo auth.IRepository
	tx   *authTransaction
}

func (u authUnit) UnitOfWork(ctx context.Context) (context.Context, auth.IRepository, postgres.UoW, error) {
	return postgres.ContextWithTransaction(ctx, u.tx), u.repo, u.tx, nil
}

type authQueue struct {
	worker.IEnqueuer
	tx   pgx.Tx
	args river.JobArgs
	err  error
}

func (q *authQueue) EnqueueTx(_ context.Context, tx pgx.Tx, args river.JobArgs) error {
	q.tx = tx
	q.args = args
	return q.err
}

func TestAuthPublicMailIsDurableBeforeAccountLookup(t *testing.T) {
	for _, reset := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(map[bool]string{false: "signup", true: "reset"}[reset]+map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
				uc, repo, _, notifier := newSignupUC(t)
				tx := &authTransaction{}
				q := &authQueue{IEnqueuer: worker.NewEnqueuer()}
				if fail {
					q.err = errors.New("queue down")
				}
				cipher, err := secret.New(strings.Repeat("ab", 32))
				require.NoError(t, err)
				uc.SetDeliveryQueue(q, cipher, authUnit{repo, tx})
				var stored postgres.CreateTemporalCodeParams
				repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
					stored = p
					return postgres.TemporalCode{}, nil
				})
				if reset {
					err = uc.ForgotPassword(context.Background(), "person@example.test")
				} else {
					err = uc.BeginEmailRegistration(context.Background(), "person@example.test", "")
				}
				require.NotContains(t, string(stored.Data), "person@example.test")
				require.Same(t, tx, q.tx)
				require.Equal(t, !fail, tx.committed)
				require.True(t, tx.restored)
				require.Zero(t, notifier.calls)
				if fail {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestAuthDeliveryRetriesLookupAndKeepsAcceptedRequest(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	cipher, err := secret.New(strings.Repeat("ab", 32))
	require.NoError(t, err)
	q := &authQueue{IEnqueuer: worker.NewEnqueuer()}
	uc.SetDeliveryQueue(q, cipher, authUnit{repo, &authTransaction{}})
	var stored postgres.CreateTemporalCodeParams
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateTemporalCodeParams) (postgres.TemporalCode, error) {
		stored = p
		return postgres.TemporalCode{}, nil
	})
	require.NoError(t, uc.ForgotPassword(context.Background(), "person@example.test"))
	id := q.args.(jobsModel.NotifyArgs).AuthDeliveryID
	require.NotNil(t, id)
	row := postgres.TemporalCode{ID: stored.ID, Code: stored.Code, Data: stored.Data, Type: stored.Type, ExpiresAt: stored.ExpiresAt}
	repo.EXPECT().GetTemporalCodeByCode(gomock.Any(), stored.Code).Return(row, nil).Times(2)
	down := errors.New("temporary database loss")
	gomock.InOrder(
		repo.EXPECT().GetUserByEmail(gomock.Any(), "person@example.test").Return(postgres.User{}, down),
		repo.EXPECT().GetUserByEmail(gomock.Any(), "person@example.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Email: "person@example.test", Status: "active"}, nil),
	)
	require.ErrorIs(t, uc.ProcessAuthDelivery(context.Background(), *id), down)
	require.Zero(t, notifier.calls)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil)
	repo.EXPECT().DeleteTemporalCode(gomock.Any(), stored.ID).Return(int64(1), nil)
	require.NoError(t, uc.ProcessAuthDelivery(context.Background(), *id))
	require.Equal(t, 1, notifier.calls, "retry must not be suppressed by the public request quota")
}

func TestAuthQueueFailureDoesNotConsumeRecipientQuota(t *testing.T) {
	uc, repo, _, _ := newSignupUC(t)
	cipher, err := secret.New(strings.Repeat("ab", 32))
	require.NoError(t, err)
	tx := &authTransaction{}
	q := &authQueue{IEnqueuer: worker.NewEnqueuer(), err: errors.New("queue unavailable")}
	uc.SetDeliveryQueue(q, cipher, authUnit{repo, tx})
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil).Times(2)
	require.Error(t, uc.ForgotPassword(context.Background(), "retry@example.test"))
	q.err = nil
	require.NoError(t, uc.ForgotPassword(context.Background(), "retry@example.test"))
	require.True(t, tx.committed, "a retry after queue failure must actually enqueue, not silently hit the mail quota")
}
