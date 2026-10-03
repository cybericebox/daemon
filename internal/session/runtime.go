package session

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/pkg/secret"
)

// SeenWindow is the span in which the first request of a session writes last_seen at once and the rest only
// update memory.
const SeenWindow = 30 * time.Second

// Runtime is everything one replica holds for sessions: the cookie codec, the revoked-session set and the
// last_seen batching.
type Runtime struct {
	Codec       *Codec
	Revocations *Revocations
	Seen        *Seen
}

// RuntimeConfig is what NewRuntime needs.
type RuntimeConfig struct {
	// Sealer is the keyring that encrypts the cookie (SESSION_ENCRYPTION_KEY).
	Sealer secret.Sealer
	Source RevocationSource
	// StaleAfter is how long the revocation poll may keep failing (SESSION_REVOCATION_STALE_AFTER).
	StaleAfter time.Duration
}

// NewRuntime builds the session machinery of one replica. The last_seen writer is bound later by the use case
// that owns the writes (Seen.SetWriter).
func NewRuntime(cfg RuntimeConfig) *Runtime {
	return &Runtime{
		Codec:       NewCodec(cfg.Sealer),
		Revocations: NewRevocations(cfg.Source, DefaultRevocationConfig(cfg.StaleAfter)),
		Seen:        NewSeen(nil, SeenWindow),
	}
}

// Start loads the revoked sessions (it must succeed before the replica serves), then runs the poll, the
// cleanup and the last_seen sweep until ctx ends.
func (r *Runtime) Start(ctx context.Context) error {
	if err := r.Revocations.Load(ctx); err != nil {
		return err
	}
	go r.Revocations.Run(ctx)
	go r.Seen.Run(ctx)
	return nil
}

// Stop writes the last_seen times still held in memory; the replica calls it on shutdown.
func (r *Runtime) Stop(ctx context.Context) { r.Seen.Flush(ctx) }
