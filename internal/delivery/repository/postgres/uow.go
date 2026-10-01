package postgres

//go:generate go run go.uber.org/mock/mockgen -destination mocks/mock_uow.go -package postgresMocks github.com/cybericebox/daemon/internal/delivery/repository/postgres UoW
//go:generate go run go.uber.org/mock/mockgen -destination mocks/mock_querier.go -package postgresMocks github.com/cybericebox/daemon/internal/delivery/repository/postgres Querier

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/internal/model"
)

// UoW is the transaction handle returned to business logic. Save persists the
// unit, Restore reverts it. Restore after a successful Save is a no-op, so
// `defer uow.Restore()` is always safe.
type UoW interface {
	Save() error
	Restore() error
}

// txHandle is the minimal commit/rollback seam (satisfied by pgx.Tx), so the
// done-flag logic is unit-testable without a real database.
type txHandle interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type uow struct {
	ctx  context.Context
	tx   txHandle
	done bool
}

func newUoW(ctx context.Context, tx txHandle) *uow {
	return &uow{ctx: ctx, tx: tx}
}

func (u *uow) Save() error {
	if u.done {
		return nil
	}
	if err := u.tx.Commit(u.ctx); err != nil {
		return err
	}
	u.done = true
	return nil
}

func (u *uow) Restore() error {
	if u.done {
		return nil
	}
	u.done = true
	return u.tx.Rollback(u.ctx)
}

// UoWFactory hides the *DB from the business layer. A single factory is built
// once (NewUoWFactory) and handed to every useCase package, which mints its own
// typed worker from it via BuildUnitOfWorker — the db never leaks past here.
type UoWFactory struct {
	db *DB
}

func newUoWFactory(db *DB) *UoWFactory {
	return &UoWFactory{db: db}
}

// NewUoWFactory builds a factory straight from a pool, for integration tests
// that drive a use case against a real database without the full repository.
func NewUoWFactory(pool *pgxpool.Pool) *UoWFactory {
	return newUoWFactory(NewDB(pool))
}

// IUnitOfWorker is the business-facing UoW port: open a tx, get the narrowed
// repo R plus the UoW handle. Declared once here; each useCase aliases it with
// its own R (e.g. `type IUnitOfWorker = postgres.IUnitOfWorker[IRepository]`),
// so no useCase re-spells the whole signature.
type IUnitOfWorker[R any] interface {
	UnitOfWork(ctx context.Context) (ctxt context.Context, repo R, uow UoW, err error)
}

// NewUnitOfWorker mints a worker narrowed to R with no per-useCase bind. The
// narrowing is a runtime assert (any(q).(R)) — safe because every useCase repo
// port R is a subset of Querier by construction, so the concrete *Queries always
// implements it. R MUST be an interface type. Call it at wiring time as
// postgres.NarrowUnitOfWorker[setting.IRepository](factory).
func NewUnitOfWorker[R any](f *UoWFactory) *UnitOfWorker[R] {
	return &UnitOfWorker[R]{db: f.db, bind: func(q Querier) R { return any(q).(R) }}
}

// UnitOfWorker opens Units of Work that expose only R. R is fixed once, when the
// owning useCase is constructed, so the useCase sees only the methods it asked
// for — not the full Querier.
type UnitOfWorker[R any] struct {
	db   *DB
	bind func(Querier) R
}

// UnitOfWork opens a fresh transaction, enriches ctx with it (so Q(ctx) joins),
// and returns the narrowed tx-bound repo (R) plus the UoW handle.
func (w *UnitOfWorker[R]) UnitOfWork(ctx context.Context) (context.Context, R, UoW, error) {
	var zero R
	tx, err := w.db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ctx, zero, nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to begin unit of work").
			Err()
	}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	return txCtx, w.bind(w.db.q.WithTx(tx)), newUoW(txCtx, tx), nil
}
