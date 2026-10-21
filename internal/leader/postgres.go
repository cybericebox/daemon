package leader

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/internal/advisory"
)

// BackgroundKey is the advisory lock key of the replica that runs the background loops.
func BackgroundKey() int64 { return advisory.Key("leader", "background") }

// PostgresLock is the leadership lock on a Postgres advisory lock key.
type PostgresLock struct {
	Pool *pgxpool.Pool
	Key  int64
}

func (l PostgresLock) TryLock(ctx context.Context) (Held, bool, error) {
	held, ok, err := advisory.TryLock(ctx, l.Pool, l.Key)
	if err != nil || !ok {
		return nil, false, err
	}
	return held, true, nil
}
