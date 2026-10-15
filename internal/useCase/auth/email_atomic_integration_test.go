package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/worker"
)

type liveAuthQueue struct {
	worker.IEnqueuer
	client *river.Client[pgx.Tx]
	fail   bool
}

func (q *liveAuthQueue) EnqueueTx(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	if q.fail {
		return errors.New("queue unavailable")
	}
	_, err := q.client.InsertTx(ctx, tx, args, nil)
	return err
}

func TestEmailChangeAndNotificationAreAtomicInPostgres(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	driver := riverpgxv5.New(db.Pool)
	migrator, err := rivermigrate.New(driver, nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	client, err := river.NewClient(driver, &river.Config{})
	require.NoError(t, err)
	queue := &liveAuthQueue{IEnqueuer: worker.NewEnqueuer(), client: client, fail: true}
	cipher, err := secret.New(strings.Repeat("ab", 32))
	require.NoError(t, err)
	factory := postgres.NewUoWFactory(db.Pool)
	dispatcher := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: db.Queries, Enqueuer: queue, Cipher: cipher, UnitOfWorker: postgres.NewUnitOfWorker[dispatcherUseCase.Repository](factory)})
	uc := auth.NewAuthUseCase(auth.Dependencies{Repo: db.Queries, Notifier: dispatcher, Config: config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")}})
	uc.SetDeliveryQueue(queue, cipher, postgres.NewUnitOfWorker[auth.IRepository](factory))
	id := uuid.Must(uuid.NewV7())
	_, err = userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(id, "old@example.test", time.Now()))
	require.NoError(t, err)
	data, err := json.Marshal(temporalCodeModel.TemporalEmailChangeCodeData{UserID: id, Email: "new@example.test"})
	require.NoError(t, err)
	codeID := uuid.Must(uuid.NewV7())
	err = temporalCodeRepo.New(db.Queries).Create(ctx, temporalCodeModel.NewCode(codeID, auth.ExportHashTemporalCode(rawCode), temporalCodeModel.EmailChangeCodeType, data, time.Now().Add(time.Hour)))
	require.NoError(t, err)
	encoded := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(rawCode)), "=")
	require.Error(t, uc.ConfirmEmailChange(ctx, encoded))
	user, err := db.Queries.GetUserByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "old@example.test", user.Email)
	_, err = db.Queries.GetTemporalCodeByCode(ctx, auth.ExportHashTemporalCode(rawCode))
	require.NoError(t, err, "the token remains usable after rollback")
	var jobs int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='notify'").Scan(&jobs))
	require.Zero(t, jobs)
	queue.fail = false
	require.NoError(t, uc.ConfirmEmailChange(ctx, encoded))
	user, err = db.Queries.GetUserByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "new@example.test", user.Email)
	_, err = db.Queries.GetTemporalCodeByCode(ctx, auth.ExportHashTemporalCode(rawCode))
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='notify'").Scan(&jobs))
	require.Equal(t, 1, jobs)
	var dispatches, payloads int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM notification_dispatches WHERE recipient_user_id=$1", id).Scan(&dispatches))
	require.Equal(t, 1, dispatches)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM temporal_codes WHERE type=$1", temporalCodeModel.NotificationPayloadCodeType).Scan(&payloads))
	require.Equal(t, 1, payloads)
}

func TestAuthCommandHandoffAndConsumptionAreAtomicInPostgres(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	driver := riverpgxv5.New(db.Pool)
	migrator, err := rivermigrate.New(driver, nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	client, err := river.NewClient(driver, &river.Config{})
	require.NoError(t, err)
	queue := &liveAuthQueue{IEnqueuer: worker.NewEnqueuer(), client: client}
	cipher, err := secret.New(strings.Repeat("ab", 32))
	require.NoError(t, err)
	factory := postgres.NewUoWFactory(db.Pool)
	dispatcher := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{Repo: db.Queries, Enqueuer: queue, Cipher: cipher, UnitOfWorker: postgres.NewUnitOfWorker[dispatcherUseCase.Repository](factory)})
	uc := auth.NewAuthUseCase(auth.Dependencies{Repo: db.Queries, Notifier: dispatcher, Config: config.AuthConfig{TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")}})
	uc.SetDeliveryQueue(queue, cipher, postgres.NewUnitOfWorker[auth.IRepository](factory))
	id := uuid.Must(uuid.NewV7())
	_, err = userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(id, "owner@example.test", time.Now()))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, "UPDATE users SET status='active' WHERE id=$1", id)
	require.NoError(t, err)
	require.NoError(t, uc.ForgotPassword(ctx, "owner@example.test"))
	var commandID uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT (args->>'auth_delivery_id')::uuid FROM river_job WHERE args ? 'auth_delivery_id'").Scan(&commandID))
	queue.fail = true
	require.Error(t, uc.ProcessAuthDelivery(ctx, commandID))
	var dispatches, commands, resets int
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM notification_dispatches WHERE recipient_user_id=$1", id).Scan(&dispatches))
	require.Zero(t, dispatches)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM temporal_codes WHERE type=$1", temporalCodeModel.AuthDeliveryCodeType).Scan(&commands))
	require.Equal(t, 1, commands)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM temporal_codes WHERE type=$1", temporalCodeModel.PasswordResettingCodeType).Scan(&resets))
	require.Zero(t, resets)
	queue.fail = false
	require.NoError(t, uc.ProcessAuthDelivery(ctx, commandID))
	require.NoError(t, uc.ProcessAuthDelivery(ctx, commandID), "a replay of the consumed command does not enqueue again")
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM notification_dispatches WHERE recipient_user_id=$1", id).Scan(&dispatches))
	require.Equal(t, 1, dispatches)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM temporal_codes WHERE type=$1", temporalCodeModel.AuthDeliveryCodeType).Scan(&commands))
	require.Zero(t, commands)
	require.NoError(t, db.Pool.QueryRow(ctx, "SELECT count(*) FROM temporal_codes WHERE type=$1", temporalCodeModel.PasswordResettingCodeType).Scan(&resets))
	require.Equal(t, 1, resets)
}
