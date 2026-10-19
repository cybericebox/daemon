package event

import (
	"context"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

const (
	// liveScreenTokensTTL: the known set is re-read this often, so links
	// issued or revoked on another instance are picked up.
	liveScreenTokensTTL = 30 * time.Second
	// liveScreenTokensMinReload bounds the reads made-up tokens and a failing
	// database can cause: at most one read attempt per this interval.
	liveScreenTokensMinReload = 5 * time.Second
	// liveScreenTokensReadTimeout bounds one read; it does not depend on the
	// request that triggered it.
	liveScreenTokensReadTimeout = 3 * time.Second
)

// liveScreenTokenSnapshot is one successful read of the working links;
// generation is the set's generation the read started at.
type liveScreenTokenSnapshot struct {
	hashes     map[string]*time.Time
	loadedAt   time.Time
	generation int64
}

// liveScreenTokenSet caches the token hashes of the working screen links
// (one per event at most), with their expiry. Readers never wait on a lock:
// a read replaces the whole snapshot, and concurrent reloads share one
// database read. Issue, regenerate and revoke on this instance mark it stale.
type liveScreenTokenSet struct {
	current atomic.Pointer[liveScreenTokenSnapshot]
	// generation grows with every local change of the links.
	generation atomic.Int64
	// lastAttempt (unix ns) and attemptGeneration throttle read attempts,
	// successful or not; a local change allows one attempt at once.
	lastAttempt       atomic.Int64
	attemptGeneration atomic.Int64
	reads             singleflight.Group
}

func (s *liveScreenTokenSet) invalidate() {
	s.generation.Add(1)
}

// IsActiveLiveScreenToken tells whether token belongs to a working screen
// link (any event). The screen request limiter gives only such links their
// own budget; it is not an access check (ResolveLiveScreenToken is).
func (u *EventUseCase) IsActiveLiveScreenToken(ctx context.Context, token string, now time.Time) bool {
	if !eventContentModel.LiveScreenTokenWellFormed(token) {
		return false
	}
	hash := string(eventContentModel.HashLiveScreenToken(token))
	set := u.liveScreenTokens
	snapshot := set.current.Load()
	if snapshot == nil || snapshot.generation != set.generation.Load() || now.Sub(snapshot.loadedAt) >= liveScreenTokensTTL {
		snapshot = u.reloadLiveScreenTokens(ctx, now)
	}
	expires, found := lookupLiveScreenToken(snapshot, hash)
	// A token missing from the set may be a link issued on another instance
	// since the last read: read again (within the attempt bound).
	if !found {
		snapshot = u.reloadLiveScreenTokens(ctx, now)
		expires, found = lookupLiveScreenToken(snapshot, hash)
	}
	return found && (expires == nil || expires.After(now))
}

func lookupLiveScreenToken(snapshot *liveScreenTokenSnapshot, hash string) (*time.Time, bool) {
	if snapshot == nil {
		return nil, false
	}
	expires, found := snapshot.hashes[hash]
	return expires, found
}

// reloadLiveScreenTokens reads the set once for all concurrent callers and
// returns the newest snapshot. The read outlives the triggering request (a
// cancelled request cannot spoil it) with its own timeout. Only a successful
// read replaces the snapshot; a failed one keeps the previous set, and the
// next attempt waits for the attempt bound (a local change of the links
// allows one attempt at once).
func (u *EventUseCase) reloadLiveScreenTokens(ctx context.Context, now time.Time) *liveScreenTokenSnapshot {
	set := u.liveScreenTokens
	_, _, _ = set.reads.Do("read", func() (any, error) {
		generation := set.generation.Load()
		if generation == set.attemptGeneration.Load() && now.Sub(time.Unix(0, set.lastAttempt.Load())) < liveScreenTokensMinReload {
			return nil, nil
		}
		set.lastAttempt.Store(now.UnixNano())
		set.attemptGeneration.Store(generation)
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveScreenTokensReadTimeout)
		defer cancel()
		hashes, err := u.liveScreens.ActiveTokenHashes(readCtx, now)
		if err != nil {
			return nil, err
		}
		set.current.Store(&liveScreenTokenSnapshot{hashes: hashes, loadedAt: now, generation: generation})
		return nil, nil
	})
	return set.current.Load()
}
