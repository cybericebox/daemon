package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/token"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
)

type invitationQueue struct {
	worker.IEnqueuer
	client *river.Client[pgx.Tx]
	fail   bool
}

func (q *invitationQueue) EnqueueTx(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	if q.fail {
		return errors.New("River insert unavailable")
	}
	_, err := q.client.InsertTx(ctx, tx, args, nil)
	return err
}
func TestInvitationAccountLinkDispatchAndJobShareCommit(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	migrator, err := rivermigrate.New(riverpgxv5.New(db.Pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	client, err := river.NewClient(riverpgxv5.New(db.Pool), &river.Config{})
	require.NoError(t, err)
	queue := &invitationQueue{IEnqueuer: worker.NewEnqueuer(), client: client, fail: true}
	cipher, err := secret.New(strings.Repeat("ab", 32))
	require.NoError(t, err)
	factory := postgres.NewUoWFactory(db.Pool)
	dispatcher := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: db.Queries, Enqueuer: queue, Cipher: cipher, UnitOfWorker: postgres.NewUnitOfWorker[dispatcherUseCase.Repository](factory)})
	uc := auth.NewAuthUseCase(auth.Dependencies{Repo: db.Queries, Token: token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}), Notifier: dispatcher, Sessions: testSessions(t), Config: config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")}})
	uc.SetDeliveryQueue(queue, cipher, postgres.NewUnitOfWorker[auth.IRepository](factory))
	err = uc.InviteUser(inviteCtx(rbac.RoleSuperAdmin), "new@example.org", rbac.RoleSuperAdmin, "New", "Operator")
	require.Error(t, err)
	for _, table := range []string{"users", "temporal_codes", "notification_dispatches", "river_job"} {
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n))
		require.Zero(t, n, table+" must roll back with failed queue")
	}
	queue.fail = false
	require.NoError(t, uc.InviteUser(inviteCtx(rbac.RoleSuperAdmin), "new@example.org", rbac.RoleSuperAdmin, "New", "Operator"))
	var status string
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT status FROM users WHERE email='new@example.org'").Scan(&status))
	require.Equal(t, "incomplete", status)
	var n int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='notify'").Scan(&n))
	require.Equal(t, 1, n)
}
