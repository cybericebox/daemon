package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type txKey struct{}

// Conns is what the DB runs on: the statements sqlc needs plus BeginTx for the unit of work. A *pgxpool.Pool
// is one; the repository uses an *AcquireLimited, so the wait for a connection is bounded.
type Conns interface {
	DBTX
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// DB is a transaction-aware wrapper around the connections + *Queries.
// Use Q(ctx) instead of accessing Queries directly so that a transaction
// injected into ctx by the Unit of Work is forwarded to every query.
type DB struct {
	pool Conns
	q    *Queries
}

func NewDB(pool Conns) *DB {
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
