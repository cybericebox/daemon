// Package leader makes exactly one replica run the background loops that talk to the laboratory agents or write
// shared state on a timer (agent monitoring, maintenance polling, the journal's periodic checks). Request handling
// and the job workers run on every replica; only these loops have a single owner.
//
// The owner is whoever holds a Postgres session advisory lock on a connection of its own. A replica that does not
// hold it retries every Retry. A holder whose connection dies loses the lock (the server drops it with the
// session), notices at its next health check, stops the loops and goes back to waiting. A holder that shuts down
// stops the loops first and then closes the session, so another replica takes over at once.
package leader

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// Held is a lock taken by Lock.
type Held interface {
	// Alive fails when the lock's session is gone.
	Alive(ctx context.Context) error
	Release()
}

// Lock tries to take the leadership lock without waiting.
type Lock interface {
	TryLock(ctx context.Context) (Held, bool, error)
}

const defaultInterval = 5 * time.Second

type Config struct {
	// Name labels the log lines.
	Name string
	// Retry is how often a non-leader tries to take the lock; 5 seconds by default.
	Retry time.Duration
	// Check is how often the leader checks that its session lives; 5 seconds by default.
	Check time.Duration
}

type Elector struct {
	lock Lock
	cfg  Config
}

func New(lock Lock, cfg Config) *Elector {
	if cfg.Retry <= 0 {
		cfg.Retry = defaultInterval
	}
	if cfg.Check <= 0 {
		cfg.Check = defaultInterval
	}
	return &Elector{lock: lock, cfg: cfg}
}

// Run waits for leadership and calls lead while it holds it, until ctx ends. lead must return when its context
// ends; the context ends when leadership is lost. A lead that returns on its own steps the replica down for Retry.
func (e *Elector) Run(ctx context.Context, lead func(ctx context.Context)) {
	for ctx.Err() == nil {
		held, ok, err := e.lock.TryLock(ctx)
		if err != nil && ctx.Err() == nil {
			log.Warn().Err(err).Str("role", e.cfg.Name).Msg("Leader election: cannot try the lock")
		}
		if err == nil && ok {
			log.Info().Str("role", e.cfg.Name).Msg("Leader election: this replica is the leader")
			e.lead(ctx, held, lead)
			log.Info().
				Str("role", e.cfg.Name).
				Msg("Leader election: this replica is no longer the leader")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.cfg.Retry):
		}
	}
}

func (e *Elector) lead(ctx context.Context, held Held, lead func(context.Context)) {
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lead(lctx)
	}()
	ticker := time.NewTicker(e.cfg.Check)
	defer ticker.Stop()
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case <-done:
			break wait
		case <-ticker.C:
			check, stop := context.WithTimeout(ctx, e.cfg.Check)
			err := held.Alive(check)
			stop()
			if err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Str("role", e.cfg.Name).Msg("Leader election: the lock session is lost")
				break wait
			}
		}
	}
	// The loops stop before the lock is given up, so two replicas never run them side by side by our doing.
	cancel()
	<-done
	held.Release()
}
