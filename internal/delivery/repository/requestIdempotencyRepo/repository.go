// Package requestIdempotencyRepo persists short-lived, cross-instance request
// replay records. It deliberately stores no domain-specific request data.
package requestIdempotencyRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type Queries interface {
	ReserveRequestIdempotency(context.Context, postgres.ReserveRequestIdempotencyParams) ([]postgres.RequestIdempotency, error)
	GetRequestIdempotency(context.Context, postgres.GetRequestIdempotencyParams) (postgres.RequestIdempotency, error)
	CompleteRequestIdempotency(context.Context, postgres.CompleteRequestIdempotencyParams) (int64, error)
	DeleteExpiredRequestIdempotency(context.Context, time.Time) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

type Key struct {
	OwnerID        uuid.UUID
	Scope          string
	IdempotencyKey uuid.UUID
}

type Record struct {
	Key
	RequestHash  []byte
	Completed    bool
	ResponseCode int32
	ResponseBody []byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

func (r *Repository) Reserve(ctx context.Context, key Key, requestHash []byte, now, expiresAt time.Time) (Record, bool, error) {
	rows, err := r.q.ReserveRequestIdempotency(ctx, postgres.ReserveRequestIdempotencyParams{OwnerID: key.OwnerID, Scope: key.Scope, Key: key.IdempotencyKey, RequestHash: requestHash, CreatedAt: now, ExpiresAt: expiresAt})
	if err != nil {
		return Record{}, false, err
	}
	if len(rows) == 1 {
		return toRecord(rows[0]), true, nil
	}
	row, err := r.q.GetRequestIdempotency(ctx, postgres.GetRequestIdempotencyParams{OwnerID: key.OwnerID, Scope: key.Scope, Key: key.IdempotencyKey})
	if err != nil {
		return Record{}, false, err
	}
	return toRecord(row), false, nil
}

func (r *Repository) Complete(ctx context.Context, record Record, responseCode int32, responseBody []byte) (bool, error) {
	n, err := r.q.CompleteRequestIdempotency(ctx, postgres.CompleteRequestIdempotencyParams{OwnerID: record.OwnerID, Scope: record.Scope, Key: record.IdempotencyKey, RequestHash: record.RequestHash, ResponseStatus: pgtype.Int4{Int32: responseCode, Valid: true}, ResponseBody: responseBody})
	return n == 1, err
}

func (r *Repository) CleanupExpired(ctx context.Context, now time.Time) error {
	_, err := r.q.DeleteExpiredRequestIdempotency(ctx, now)
	return err
}

func toRecord(row postgres.RequestIdempotency) Record {
	return Record{Key: Key{OwnerID: row.OwnerID, Scope: row.Scope, IdempotencyKey: row.Key}, RequestHash: row.RequestHash, Completed: row.Completed, ResponseCode: row.ResponseStatus.Int32, ResponseBody: row.ResponseBody, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
}
