// Package signalOutboxRepo persists durable system signals through sqlc.
package signalOutboxRepo

import (
	"context"
	"fmt"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CreateQueries is the narrow sqlc surface required to publish a signal.
type CreateQueries interface {
	CreateSignalOutbox(ctx context.Context, arg postgres.CreateSignalOutboxParams) (postgres.SignalOutbox, error)
}

// Repository implements the signal publisher's durable outbox port.
type Repository struct {
	q CreateQueries
}

func New(q CreateQueries) *Repository {
	return &Repository{q: q}
}

// Create makes the signal immediately available to the outbox worker. When q
// is transaction-bound, this insert commits atomically with its domain change.
func (r *Repository) Create(ctx context.Context, signal signalModel.Signal) error {
	_, err := r.q.CreateSignalOutbox(ctx, postgres.CreateSignalOutboxParams{
		ID:          signal.ID,
		SignalType:  string(signal.Type),
		OccurredAt:  signal.OccurredAt,
		Payload:     []byte(signal.Payload),
		AvailableAt: signal.OccurredAt,
		CreatedAt:   signal.OccurredAt,
	})
	return err
}

// ExecutionQueries is the sqlc surface used by the independent hook runner.
type ExecutionQueries interface {
	ClaimSignalOutboxBatch(context.Context, postgres.ClaimSignalOutboxBatchParams) ([]postgres.SignalOutbox, error)
	EnsureSignalHookExecution(context.Context, postgres.EnsureSignalHookExecutionParams) error
	ClaimSignalHookExecution(context.Context, postgres.ClaimSignalHookExecutionParams) (postgres.SignalHookExecution, error)
	CompleteSignalHookExecution(context.Context, postgres.CompleteSignalHookExecutionParams) (int64, error)
	RetrySignalHookExecution(context.Context, postgres.RetrySignalHookExecutionParams) (int64, error)
	CountIncompleteSignalHookExecutions(context.Context, uuid.UUID) (int64, error)
	CompleteSignalOutbox(context.Context, postgres.CompleteSignalOutboxParams) (int64, error)
	RetrySignalOutbox(context.Context, postgres.RetrySignalOutboxParams) (int64, error)
}

// ExecutionStore maps sqlc rows to the signal processor's storage boundary.
type ExecutionStore struct{ q ExecutionQueries }

func NewExecutionStore(q ExecutionQueries) *ExecutionStore { return &ExecutionStore{q: q} }

func (r *ExecutionStore) ClaimSignals(ctx context.Context, now time.Time, batchSize int) ([]signalModel.Signal, error) {
	rows, err := r.q.ClaimSignalOutboxBatch(ctx, postgres.ClaimSignalOutboxBatchParams{
		NowAt:     pgtype.Timestamptz{Time: now, Valid: true},
		BatchSize: int32(batchSize),
	})
	if err != nil {
		return nil, err
	}
	signals := make([]signalModel.Signal, 0, len(rows))
	for _, row := range rows {
		signals = append(signals, signalModel.Signal{
			ID: row.ID, Type: signalModel.Type(row.SignalType), OccurredAt: row.OccurredAt, Payload: row.Payload,
		})
	}
	return signals, nil
}

func (r *ExecutionStore) EnsureHookExecution(ctx context.Context, signalID uuid.UUID, hookName string, now time.Time) error {
	return r.q.EnsureSignalHookExecution(ctx, postgres.EnsureSignalHookExecutionParams{
		SignalID: signalID, HookName: hookName, AvailableAt: now, CreatedAt: now,
	})
}

func (r *ExecutionStore) ClaimHookExecution(ctx context.Context, signalID uuid.UUID, hookName string, now time.Time) (bool, error) {
	_, err := r.q.ClaimSignalHookExecution(ctx, postgres.ClaimSignalHookExecutionParams{
		SignalID: signalID, HookName: hookName, NowAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (r *ExecutionStore) CompleteHookExecution(ctx context.Context, signalID uuid.UUID, hookName string, now time.Time) error {
	affected, err := r.q.CompleteSignalHookExecution(ctx, postgres.CompleteSignalHookExecutionParams{
		SignalID: signalID, HookName: hookName, CompletedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	return requireOneAffected("complete signal hook execution", affected, err)
}

func (r *ExecutionStore) RetryHookExecution(ctx context.Context, signalID uuid.UUID, hookName string, availableAt time.Time, message string) error {
	affected, err := r.q.RetrySignalHookExecution(ctx, postgres.RetrySignalHookExecutionParams{
		SignalID: signalID, HookName: hookName, AvailableAt: availableAt, LastError: message,
	})
	return requireOneAffected("retry signal hook execution", affected, err)
}

func (r *ExecutionStore) CountIncompleteHookExecutions(ctx context.Context, signalID uuid.UUID) (int64, error) {
	return r.q.CountIncompleteSignalHookExecutions(ctx, signalID)
}

func (r *ExecutionStore) CompleteSignal(ctx context.Context, signalID uuid.UUID, now time.Time) error {
	affected, err := r.q.CompleteSignalOutbox(ctx, postgres.CompleteSignalOutboxParams{
		ID: signalID, CompletedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	return requireOneAffected("complete signal outbox", affected, err)
}

func (r *ExecutionStore) RetrySignal(ctx context.Context, signalID uuid.UUID, availableAt time.Time, message string) error {
	affected, err := r.q.RetrySignalOutbox(ctx, postgres.RetrySignalOutboxParams{
		ID: signalID, AvailableAt: availableAt, LastError: message,
	})
	return requireOneAffected("retry signal outbox", affected, err)
}

func requireOneAffected(operation string, affected int64, err error) error {
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("signal outbox: %s affected %d rows", operation, affected)
	}
	return nil
}
