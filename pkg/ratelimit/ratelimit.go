// Package ratelimit holds the in-memory limiters of the authentication flows:
// a sliding-window counter (mail per recipient, request bursts) and a failure
// lockout with growing delay (password guessing).
//
// State lives in the process: with several replicas the effective limit is
// per replica. Both types are bounded in memory: expired keys are swept and,
// past maxKeys, arbitrary keys are evicted (an attacker flooding fresh keys
// can at worst reset other keys' counters probabilistically, never grow the
// map).
package ratelimit

import (
	"sync"
	"time"
)

// maxKeys bounds the keys one limiter remembers.
const maxKeys = 100_000

// Clock returns the current time; tests substitute their own.
type Clock func() time.Time

// Window allows at most limit events per key within any window (sliding log).
type Window struct {
	limit  int
	window time.Duration
	now    Clock

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

// NewWindow returns a limiter of limit events per window per key.
func NewWindow(limit int, window time.Duration) *Window {
	return &Window{limit: limit, window: window, now: time.Now, hits: map[string][]time.Time{}}
}

// WithClock replaces the clock (tests).
func (w *Window) WithClock(now Clock) *Window {
	w.now = now
	return w
}

// Allow records an event for key and reports whether it is within the limit.
// When it is not, retryAfter is how long until the oldest counted event leaves
// the window; a denied event is NOT recorded (hammering does not extend the wait).
func (w *Window) Allow(key string) (allowed bool, retryAfter time.Duration) {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sweep(now)

	cutoff := now.Add(-w.window)
	recent := w.hits[key]
	i := 0
	for i < len(recent) && !recent[i].After(cutoff) {
		i++
	}
	recent = recent[i:]
	if len(recent) >= w.limit {
		w.hits[key] = recent
		return false, recent[0].Add(w.window).Sub(now)
	}
	w.makeRoom(key)
	w.hits[key] = append(recent, now)
	return true, 0
}

func (w *Window) sweep(now time.Time) {
	if now.Sub(w.lastSweep) < w.window {
		return
	}
	w.lastSweep = now
	cutoff := now.Add(-w.window)
	for key, recent := range w.hits {
		if len(recent) == 0 || !recent[len(recent)-1].After(cutoff) {
			delete(w.hits, key)
		}
	}
}

func (w *Window) makeRoom(key string) {
	if _, known := w.hits[key]; known || len(w.hits) < maxKeys {
		return
	}
	for victim := range w.hits {
		delete(w.hits, victim)
		return
	}
}

// Lockout counts failures per key and, once maxFailures are reached within
// window, refuses the key for a delay that doubles with every further failure
// (baseLock, 2*baseLock, ... capped at maxLock). A success clears the key.
type Lockout struct {
	maxFailures int
	window      time.Duration
	baseLock    time.Duration
	maxLock     time.Duration
	now         Clock

	mu        sync.Mutex
	entries   map[string]*lockoutEntry
	lastSweep time.Time
}

type lockoutEntry struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

// NewLockout returns a failure lockout.
func NewLockout(maxFailures int, window, baseLock, maxLock time.Duration) *Lockout {
	return &Lockout{
		maxFailures: maxFailures, window: window, baseLock: baseLock, maxLock: maxLock,
		now: time.Now, entries: map[string]*lockoutEntry{},
	}
}

// WithClock replaces the clock (tests).
func (l *Lockout) WithClock(now Clock) *Lockout {
	l.now = now
	return l
}

// Locked reports how long key stays refused; zero means it may try.
func (l *Lockout) Locked(key string) time.Duration {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[key]; ok && now.Before(e.lockedUntil) {
		return e.lockedUntil.Sub(now)
	}
	return 0
}

// Fail records a failed attempt for key.
func (l *Lockout) Fail(key string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= maxKeys {
			for victim := range l.entries {
				delete(l.entries, victim)
				break
			}
		}
		e = &lockoutEntry{}
		l.entries[key] = e
	}
	if now.Sub(e.lastFailure) > l.window {
		e.failures = 0 // old failures are forgiven
	}
	e.failures++
	e.lastFailure = now
	if e.failures >= l.maxFailures {
		lock := l.baseLock
		for n := l.maxFailures; n < e.failures && lock < l.maxLock; n++ {
			lock *= 2
		}
		if lock > l.maxLock {
			lock = l.maxLock
		}
		e.lockedUntil = now.Add(lock)
	}
}

// Reset forgets key (a successful attempt).
func (l *Lockout) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *Lockout) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for key, e := range l.entries {
		if now.Sub(e.lastFailure) > l.window && !now.Before(e.lockedUntil) {
			delete(l.entries, key)
		}
	}
}

// Recipient limits the mail one address can be made to receive: at most
// perHour per kind of mail per hour, at least gap apart. Kinds are counted
// separately so a flood of one kind (sign-up) cannot use up the quota of an
// unrelated one (password reset).
type Recipient struct {
	gap    *Window
	hourly *Window
}

// NewRecipient returns a per-recipient mail limiter.
func NewRecipient(gap time.Duration, perHour int) *Recipient {
	return &Recipient{gap: NewWindow(1, gap), hourly: NewWindow(perHour, time.Hour)}
}

// WithClock replaces the clock (tests).
func (r *Recipient) WithClock(now Clock) *Recipient {
	r.gap.WithClock(now)
	r.hourly.WithClock(now)
	return r
}

// Allow records a mail of kind to address and reports whether it may be sent.
func (r *Recipient) Allow(kind, address string) bool {
	key := kind + "\x00" + address
	if ok, _ := r.gap.Allow(key); !ok {
		return false
	}
	ok, _ := r.hourly.Allow(key)
	return ok
}
