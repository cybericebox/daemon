package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

// DB is a transaction-aware wrapper around *pgxpool.Pool + *Queries.
// Use Q(ctx) instead of accessing Queries directly so that a transaction
// injected into ctx by the Unit of Work is forwarded to every query.
type DB struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewDB(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool, q: New(pool)}
}

// Q returns Queries backed by the active transaction stored in ctx, or the
// connection pool when no transaction is active.
func (db *DB) Q(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return db.q.WithTx(tx)
	}
	return db.q
}

// ContextWithTransaction binds the unit of work transaction to repository and queue operations.
func ContextWithTransaction(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TransactionFromContext exposes the active unit of work to transaction-aware queue operations.
func TransactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
