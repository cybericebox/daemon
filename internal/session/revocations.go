package session

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
)

// Revocation is one ended session: until ExpiresAt a cookie of it could still pass the expiry check.
type Revocation struct {
	SessionID uuid.UUID
	UserID    uuid.UUID
	RevokedAt time.Time
	ExpiresAt time.Time
}

// RevocationSource is the table of revoked sessions.
type RevocationSource interface {
	// DatabaseTime is the clock of the database; every watermark is kept in it, never in the clock of a replica.
	DatabaseTime(ctx context.Context) (time.Time, error)
	// ActiveRevocations are the rows whose cookies could still be alive.
	ActiveRevocations(ctx context.Context) ([]Revocation, error)
	// RevocationsSince are the rows revoked after since.
	RevocationsSince(ctx context.Context, since time.Time) ([]Revocation, error)
	// DeleteExpiredRevocations drops the rows past their expiry.
	DeleteExpiredRevocations(ctx context.Context) (int64, error)
}

// RevocationConfig tunes the poll.
type RevocationConfig struct {
	// Interval between polls (1 s).
	Interval time.Duration
	// Overlap is how far each poll reaches behind the watermark: it covers a transaction that commits late.
	Overlap time.Duration
	// StaleAfter is how long the poll may keep failing before the replica refuses signed-in requests rather than
	// trust a stale list.
	StaleAfter time.Duration
	// CleanupEvery is how often expired rows are deleted from the table.
	CleanupEvery time.Duration
}

// DefaultRevocationConfig is the poll of the spec: every second, with a 10 s overlap.
func DefaultRevocationConfig(staleAfter time.Duration) RevocationConfig {
	return RevocationConfig{Interval: time.Second, Overlap: 10 * time.Second, StaleAfter: staleAfter, CleanupEvery: time.Minute}
}

// Revocations is the in-memory set of revoked session ids of one replica.
type Revocations struct {
	src RevocationSource
	cfg RevocationConfig
	now func() time.Time

	mu        sync.RWMutex
	ids       map[uuid.UUID]time.Time // session id -> expiry of its cookies
	watermark time.Time               // database time the next poll continues from
	lastOK    time.Time               // local time of the last successful load or poll
	loaded    bool
}

func NewRevocations(src RevocationSource, cfg RevocationConfig) *Revocations {
	return &Revocations{src: src, cfg: cfg, now: time.Now, ids: map[uuid.UUID]time.Time{}}
}

// Load reads the rows that are not yet expired. It runs before the replica serves, so a request never meets an
// empty list that is really unread.
func (r *Revocations) Load(ctx context.Context) error {
	dbNow, err := r.src.DatabaseTime(ctx)
	if err != nil {
		return err
	}
	rows, err := r.src.ActiveRevocations(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range rows {
		r.ids[row.SessionID] = row.ExpiresAt
	}
	r.watermark = dbNow
	r.lastOK = r.now()
	r.loaded = true
	return nil
}

// Poll reads the rows revoked since the watermark minus the overlap. The session id dedupes what the overlap
// reads twice.
func (r *Revocations) Poll(ctx context.Context) error {
	r.mu.RLock()
	since := r.watermark.Add(-r.cfg.Overlap)
	r.mu.RUnlock()
	// The watermark is read before the rows: a row that commits in between is read again by the next poll.
	dbNow, err := r.src.DatabaseTime(ctx)
	if err != nil {
		return err
	}
	rows, err := r.src.RevocationsSince(ctx, since)
	if err != nil {
		return err
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range rows {
		r.ids[row.SessionID] = row.ExpiresAt
	}
	for id, expires := range r.ids {
		if expires.Before(now) {
			delete(r.ids, id)
		}
	}
	r.watermark = dbNow
	r.lastOK = now
	return nil
}

// Add records a revocation this replica wrote itself, so it takes effect here at once.
func (r *Revocations) Add(id uuid.UUID, expires time.Time) {
	r.mu.Lock()
	r.ids[id] = expires
	r.mu.Unlock()
}

// Revoked reports whether the session was ended.
func (r *Revocations) Revoked(id uuid.UUID) bool {
	r.mu.RLock()
	_, ok := r.ids[id]
	r.mu.RUnlock()
	return ok
}

// Healthy is false until the first load and whenever the poll has been failing for longer than StaleAfter: a
// replica in that state refuses signed-in requests (fail closed).
func (r *Revocations) Healthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded && r.now().Sub(r.lastOK) <= r.cfg.StaleAfter
}

// Run polls until ctx ends and deletes expired rows from the table now and then.
func (r *Revocations) Run(ctx context.Context) {
	poll := time.NewTicker(r.cfg.Interval)
	defer poll.Stop()
	cleanup := time.NewTicker(r.cfg.CleanupEvery)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			if err := r.Poll(ctx); err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Msg("Session revocation poll failed")
			}
		case <-cleanup.C:
			if _, err := r.src.DeleteExpiredRevocations(ctx); err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Msg("Session revocation cleanup failed")
			}
		}
	}
}
