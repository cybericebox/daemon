// Package advisory holds Postgres session-level advisory locks on a connection of their own. Every replica of the
// daemon shares the database, so a lock here is a lock between replicas (a sync.Mutex is only one between the
// goroutines of one process).
package advisory

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Key derives the lock key of a named thing: namespace keeps the keys of different features apart.
func Key(namespace, id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(namespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64()) //nolint:gosec // any 64 bit pattern is a valid key
}

// releaseTimeout bounds closing a lock session.
const releaseTimeout = 5 * time.Second

// Held is a lock held by one connection until Release (or until the connection dies).
type Held struct {
	conn *pgx.Conn
}

// Alive reports whether the lock's connection still answers. A session that ended has lost its lock.
func (h *Held) Alive(ctx context.Context) error { return h.conn.Ping(ctx) }

// Release ends the session, which releases the lock even when the server was unreachable for a unlock statement.
func (h *Held) Release() {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	_ = h.conn.Close(ctx)
}

// TryLock takes the lock when nobody holds it; false means another session holds it.
func TryLock(ctx context.Context, pool *pgxpool.Pool, key int64) (*Held, bool, error) {
	held, err := open(ctx, pool)
	if err != nil {
		return nil, false, err
	}
	var locked bool
	if err = held.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).
		Scan(&locked); err != nil ||
		!locked {
		held.Release()
		return nil, false, err
	}
	return held, true, nil
}

// Lock waits for the lock until ctx ends.
func Lock(ctx context.Context, pool *pgxpool.Pool, key int64) (*Held, error) {
	held, err := open(ctx, pool)
	if err != nil {
		return nil, err
	}
	if _, err = held.conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		held.Release()
		return nil, fmt.Errorf("advisory lock: %w", err)
	}
	return held, nil
}

// open takes a connection out of the pool for good: it carries a session lock, so it must never be reused.
func open(ctx context.Context, pool *pgxpool.Pool) (*Held, error) {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire lock connection: %w", err)
	}
	return &Held{conn: c.Hijack()}, nil
}
