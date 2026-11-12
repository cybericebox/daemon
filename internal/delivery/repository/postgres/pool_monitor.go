package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// PoolWatchInterval is how often the busy state of the pools is read.
const PoolWatchInterval = 15 * time.Second

// poolBusyFraction is the share of a pool in use that is worth a warning.
const poolBusyFraction = 0.8

// PoolUsage is how busy one pool is, and how often it made a statement wait or give up since the last reading.
type PoolUsage struct {
	Name     string
	Max      int32
	Acquired int32
	// Waited counts the acquisitions that found the pool empty and had to wait; Gave counts those that gave up (the
	// acquire timeout: the requests answered 503).
	Waited int64
	Gave   int64
}

// Saturated tells whether the pool is worth a warning: nearly all connections in use, or statements waited or gave up.
func (u PoolUsage) Saturated() bool {
	return u.Waited > 0 || u.Gave > 0 || (u.Max > 0 && float64(u.Acquired)/float64(u.Max) >= poolBusyFraction)
}

type poolReading struct{ waited, gave int64 }

// usageOf reads a pool and, against the previous reading, how many acquisitions waited or gave up since.
func usageOf(name string, pool *pgxpool.Pool, prev *poolReading) PoolUsage {
	s := pool.Stat()
	u := PoolUsage{Name: name, Max: s.MaxConns(), Acquired: s.AcquiredConns(),
		Waited: s.EmptyAcquireCount() - prev.waited, Gave: s.CanceledAcquireCount() - prev.gave}
	prev.waited, prev.gave = s.EmptyAcquireCount(), s.CanceledAcquireCount()
	return u
}

// Usage reads how busy the two pools are now (the counters of waits are since the process started).
func (r *PostgresRepository) Usage() []PoolUsage {
	read := func(name string, pool *pgxpool.Pool) PoolUsage {
		s := pool.Stat()
		return PoolUsage{Name: name, Max: s.MaxConns(), Acquired: s.AcquiredConns(), Waited: s.EmptyAcquireCount(), Gave: s.CanceledAcquireCount()}
	}
	return []PoolUsage{read("main", r.pool), read("job", r.jobPool)}
}

// WatchPools logs a warning, at most once per reading, when a pool is nearly full or statements waited or gave up
// for a connection since the last reading, until ctx ends. held is the number of lock sessions kept outside the pools
// (advisory locks), named in the warning because they take connections of their own.
func (r *PostgresRepository) WatchPools(ctx context.Context, every time.Duration, held func() int) {
	t := time.NewTicker(every)
	defer t.Stop()
	prev := map[string]*poolReading{"main": {}, "job": {}}
	pools := map[string]*pgxpool.Pool{"main": r.pool, "job": r.jobPool}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, name := range []string{"main", "job"} {
			u := usageOf(name, pools[name], prev[name])
			if !u.Saturated() {
				continue
			}
			ev := log.Warn().Str("pool", u.Name).Int32("max", u.Max).Int32("in_use", u.Acquired).Int64("waited", u.Waited).Int64("gave_up", u.Gave)
			if held != nil {
				ev = ev.Int("lock_sessions", held())
			}
			ev.Msg("DB pool is nearly full or statements waited for a connection: raise POSTGRES_MAX_CONNS (mind max_connections) or find what holds connections")
		}
	}
}
