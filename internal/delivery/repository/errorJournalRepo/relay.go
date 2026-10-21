package errorJournalRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

const (
	// relayChannel is the Postgres channel of the journal's live stream.
	relayChannel = "error_journal"
	closeTimeout = 5 * time.Second
	maxBackoff   = 30 * time.Second
)

// Relay is the live stream between replicas over Postgres LISTEN/NOTIFY. LISTEN needs a connection that stays
// open, so Listen takes one out of the pool for its lifetime; NOTIFY is a plain statement on the pool. The
// payload is a few ids (see errorJournalUseCase.RelayMessage), far below the 8000 byte limit.
type Relay struct{ pool *pgxpool.Pool }

func NewRelay(pool *pgxpool.Pool) *Relay { return &Relay{pool: pool} }

var _ errorJournalUseCase.Relay = (*Relay)(nil)

func (r *Relay) Publish(ctx context.Context, m errorJournalUseCase.RelayMessage) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, relayChannel, string(payload))
	return err
}

func (r *Relay) Listen(ctx context.Context, handle func(errorJournalUseCase.RelayMessage)) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := r.listen(ctx, handle)
		if ctx.Err() != nil {
			return
		}
		log.Warn().
			Err(err).
			Msg("Error journal relay: the listening connection failed, reconnecting")
		if time.Since(started) > time.Minute {
			backoff = time.Second // it ran well before: start the backoff over
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff+backoff, maxBackoff)
	}
}

func (r *Relay) listen(ctx context.Context, handle func(errorJournalUseCase.RelayMessage)) error {
	pooled, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pooled.Hijack() // a listening session never goes back to the pool
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err = conn.Exec(ctx, "LISTEN "+relayChannel); err != nil {
		return err
	}
	for {
		n, waitErr := conn.WaitForNotification(ctx)
		if waitErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return waitErr
		}
		var m errorJournalUseCase.RelayMessage
		if decodeErr := json.Unmarshal([]byte(n.Payload), &m); decodeErr != nil {
			log.Debug().Err(decodeErr).Msg("Error journal relay: unreadable message")
			continue
		}
		handle(m)
	}
}
