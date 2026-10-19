package event

import (
	"context"
	"sync"
	"time"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

const (
	// liveScreenTokensTTL: the known set is re-read this often, so links
	// issued or revoked on another instance are picked up.
	liveScreenTokensTTL = 30 * time.Second
	// liveScreenTokensMinReload bounds the reads an unknown token can cause:
	// a flood of made-up tokens costs at most one read per this interval.
	liveScreenTokensMinReload = 5 * time.Second
)

// liveScreenTokenSet caches the token hashes of the working screen links
// (one per event at most), with their expiry. Issue, regenerate and revoke
// on this instance mark it stale at once.
type liveScreenTokenSet struct {
	mu       sync.Mutex
	hashes   map[string]*time.Time
	loadedAt time.Time
	stale    bool
}

func (s *liveScreenTokenSet) invalidate() {
	s.mu.Lock()
	s.stale = true
	s.mu.Unlock()
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
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.hashes == nil || set.stale || now.Sub(set.loadedAt) >= liveScreenTokensTTL {
		u.reloadLiveScreenTokens(ctx, now)
	}
	expires, found := set.hashes[hash]
	// A token missing from the set may be a link issued on another instance
	// since the last read: read again, but not more often than the bound.
	if !found && now.Sub(set.loadedAt) >= liveScreenTokensMinReload {
		u.reloadLiveScreenTokens(ctx, now)
		expires, found = set.hashes[hash]
	}
	return found && (expires == nil || expires.After(now))
}

// reloadLiveScreenTokens reads the set; on a failed read the previous set
// stays and the next try waits for the reload bound. The caller holds mu.
func (u *EventUseCase) reloadLiveScreenTokens(ctx context.Context, now time.Time) {
	set := u.liveScreenTokens
	set.loadedAt, set.stale = now, false
	hashes, err := u.liveScreens.ActiveTokenHashes(ctx, now)
	if err != nil {
		return
	}
	set.hashes = hashes
}
