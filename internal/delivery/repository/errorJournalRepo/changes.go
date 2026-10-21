package errorJournalRepo

import (
	"context"
	"errors"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/jackc/pgx/v5"

	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

const (
	// changeLogLock serializes the writers of the change log so that seq order is commit order.
	changeLogLock = `SELECT pg_advisory_xact_lock(hashtextextended('error-journal-changes', 0))`
	changeInsert  = `INSERT INTO error_journal_changes (group_id, sample_id, is_new, origin) VALUES ($1, $2, $3, $4)`
	changeRetain  = `DELETE FROM error_journal_changes WHERE at < now() - interval '1 day'`
)

// appendChange adds one row to the change log inside tx. The lock is taken last, right before the commit, so
// it is held for no longer than the commit takes.
func appendChange(ctx context.Context, tx pgx.Tx, c errorJournalUseCase.Change) error {
	if _, err := tx.Exec(ctx, changeLogLock); err != nil {
		return err
	}
	var sample *uuid.UUID
	if c.SampleID != uuid.Nil {
		sample = &c.SampleID
	}
	_, err := tx.Exec(ctx, changeInsert, c.GroupID, sample, c.New, c.Origin)
	return err
}

// recordChange writes a change of its own transaction (a status change has no capture transaction).
func (r *Repository) recordChange(ctx context.Context, c errorJournalUseCase.Change) error {
	pool, ok := r.pool.(interface {
		Begin(context.Context) (pgx.Tx, error)
	})
	if !ok {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = appendChange(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) LatestChange(ctx context.Context) (int64, error) {
	var seq int64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM error_journal_changes`).
		Scan(&seq)
	return seq, err
}

func (r *Repository) ChangesAfter(
	ctx context.Context,
	after int64,
	limit int,
) ([]errorJournalUseCase.Change, error) {
	pool, ok := r.pool.(interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	})
	if !ok {
		return nil, errors.New("the change log needs a pool that can query")
	}
	rows, err := pool.Query(
		ctx,
		`SELECT seq, group_id, sample_id, is_new, origin FROM error_journal_changes WHERE seq > $1 ORDER BY seq LIMIT $2`,
		after,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []errorJournalUseCase.Change
	for rows.Next() {
		var c errorJournalUseCase.Change
		var sample *uuid.UUID
		if err = rows.Scan(&c.Seq, &c.GroupID, &sample, &c.New, &c.Origin); err != nil {
			return nil, err
		}
		if sample != nil {
			c.SampleID = *sample
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
