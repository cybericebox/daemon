package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/pkg/overload"
)

// AcquireLimited runs statements on a pool but waits for a free connection at most timeout: past it the call
// fails with an error wrapping overload.ErrDBBusy (answered 503), instead of queueing until the client gives up.
// Only the wait is bounded: the statement then runs on the caller's own context, however long it takes. The
// connection goes back when the call ends (Exec), when the rows are read to the end or closed (Query), after
// Scan (QueryRow), or at Commit/Rollback (Begin, BeginTx). It covers the statements sqlc (DBTX) and the unit of
// work (BeginTx) use; anything else (Acquire, CopyFrom, SendBatch) is not offered, so it cannot slip past.
type AcquireLimited struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

func NewAcquireLimited(pool *pgxpool.Pool, timeout time.Duration) *AcquireLimited {
	return &AcquireLimited{pool: pool, timeout: timeout}
}

// acquire takes a connection within the timeout. A wait cut by the timeout while the caller is still alive is
// ErrDBBusy; a caller that was cancelled or ran out of its own time gets its context error as it is.
func (a *AcquireLimited) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	waitCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	conn, err := a.pool.Acquire(waitCtx)
	if err != nil {
		if ctx.Err() == nil && waitCtx.Err() != nil {
			return nil, fmt.Errorf(
				"%w: no connection within %v: %w",
				overload.ErrDBBusy,
				a.timeout,
				err,
			)
		}
		return nil, err
	}
	return conn, nil
}

func (a *AcquireLimited) Exec(
	ctx context.Context,
	sql string,
	args ...any,
) (pgconn.CommandTag, error) {
	conn, err := a.acquire(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer conn.Release()
	return conn.Exec(ctx, sql, args...)
}

func (a *AcquireLimited) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	conn, err := a.acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		conn.Release()
		return nil, err
	}
	return &releasingRows{Rows: rows, conn: conn}, nil
}

func (a *AcquireLimited) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	conn, err := a.acquire(ctx)
	if err != nil {
		return errRow{err: err}
	}
	return &releasingRow{row: conn.QueryRow(ctx, sql, args...), conn: conn}
}

func (a *AcquireLimited) Begin(ctx context.Context) (pgx.Tx, error) {
	return a.BeginTx(ctx, pgx.TxOptions{})
}

func (a *AcquireLimited) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	conn, err := a.acquire(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, opts)
	if err != nil {
		conn.Release()
		return nil, err
	}
	return &releasingTx{Tx: tx, conn: conn}, nil
}

// releasingRows gives the connection back once the rows are done: read to the end or closed.
type releasingRows struct {
	pgx.Rows

	conn *pgxpool.Conn
	once sync.Once
}

func (r *releasingRows) release() { r.once.Do(r.conn.Release) }

func (r *releasingRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	r.release()
	return false
}

func (r *releasingRows) Close() {
	r.Rows.Close()
	r.release()
}

// releasingRow gives the connection back after Scan.
type releasingRow struct {
	row  pgx.Row
	conn *pgxpool.Conn
}

func (r *releasingRow) Scan(dest ...any) error {
	defer r.conn.Release()
	return r.row.Scan(dest...)
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// releasingTx gives the connection back at Commit or Rollback, whichever ends the transaction first.
type releasingTx struct {
	pgx.Tx

	conn *pgxpool.Conn
	once sync.Once
}

func (t *releasingTx) release() { t.once.Do(t.conn.Release) }

func (t *releasingTx) Commit(ctx context.Context) error {
	defer t.release()
	return t.Tx.Commit(ctx)
}

func (t *releasingTx) Rollback(ctx context.Context) error {
	defer t.release()
	return t.Tx.Rollback(ctx)
}
