// Package jobQueueRepo reaches the River job queue for the few cases where a use case must cancel queued work. River
// owns its tables, so they are not part of the sqlc schema and the statement is raw.
package jobQueueRepo

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Pool is the part of the connection pool the repository uses.
type Pool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Repository struct {
	pool Pool
}

func New(pool Pool) *Repository {
	return &Repository{pool: pool}
}

// cancelEventNotificationsSQL cancels the notices of one event that have not started: waiting, delayed and retrying
// ones. A running job is left to finish; at send time an event that is gone is dropped without a retry.
const cancelEventNotificationsSQL = `
UPDATE river_job
SET state = 'cancelled', finalized_at = now()
WHERE kind = 'notify'
  AND state IN ('available', 'scheduled', 'retryable')
  AND args->>'scope_event_id' = $1`

// CancelEventNotifications cancels the queued notices scoped to the event and returns how many were cancelled.
func (r *Repository) CancelEventNotifications(ctx context.Context, eventID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, cancelEventNotificationsSQL, eventID.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
