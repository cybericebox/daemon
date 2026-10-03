package session

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
)

// SeenWriter stores the last activity of a session (and of its user).
type SeenWriter interface {
	WriteSeen(ctx context.Context, sessionID, userID uuid.UUID, at time.Time) error
}

type seenEntry struct {
	userID    uuid.UUID
	windowEnd time.Time
	pending   time.Time // the latest request after the first write of the window; zero when there was none
}

// Seen batches last_seen: the first request of a window writes at once (async); later ones in the window only
// update memory; at the end of the window the latest time is written, if there was one. The write takes the
// greatest value, so a late write never moves last_seen back.
type Seen struct {
	w      SeenWriter
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[uuid.UUID]*seenEntry
	wg      sync.WaitGroup
}

func NewSeen(w SeenWriter, window time.Duration) *Seen {
	return &Seen{w: w, window: window, now: time.Now, entries: map[uuid.UUID]*seenEntry{}}
}

// SetWriter binds the store the times are written to.
func (s *Seen) SetWriter(w SeenWriter) { s.w = w }

// Touch notes a request of the session.
func (s *Seen) Touch(sessionID, userID uuid.UUID) {
	now := s.now()
	s.mu.Lock()
	e, ok := s.entries[sessionID]
	if ok && now.Before(e.windowEnd) {
		e.pending = now
		s.mu.Unlock()
		return
	}
	s.entries[sessionID] = &seenEntry{userID: userID, windowEnd: now.Add(s.window)}
	s.mu.Unlock()
	s.writeAsync(sessionID, userID, now)
}

func (s *Seen) writeAsync(sessionID, userID uuid.UUID, at time.Time) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.write(context.Background(), sessionID, userID, at)
	}()
}

func (s *Seen) write(ctx context.Context, sessionID, userID uuid.UUID, at time.Time) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.w.WriteSeen(c, sessionID, userID, at); err != nil {
		log.Debug().Err(err).Str("sessionID", sessionID.String()).Msg("Failed to write last_seen")
	}
}

// Sweep closes the windows that ended: the pending time of each is written.
func (s *Seen) Sweep() {
	now := s.now()
	type due struct {
		sessionID, userID uuid.UUID
		at                time.Time
	}
	var writes []due
	s.mu.Lock()
	for id, e := range s.entries {
		if now.Before(e.windowEnd) {
			continue
		}
		if !e.pending.IsZero() {
			writes = append(writes, due{id, e.userID, e.pending})
		}
		delete(s.entries, id)
	}
	s.mu.Unlock()
	for _, d := range writes {
		s.writeAsync(d.sessionID, d.userID, d.at)
	}
}

// Run sweeps until ctx ends.
func (s *Seen) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.Sweep()
		}
	}
}

// Flush writes every pending time at once and waits for the writes in flight; the replica calls it on SIGTERM
// before it exits.
func (s *Seen) Flush(ctx context.Context) {
	s.mu.Lock()
	type due struct {
		sessionID, userID uuid.UUID
		at                time.Time
	}
	var writes []due
	for id, e := range s.entries {
		if !e.pending.IsZero() {
			writes = append(writes, due{id, e.userID, e.pending})
		}
	}
	s.entries = map[uuid.UUID]*seenEntry{}
	s.mu.Unlock()
	for _, d := range writes {
		s.write(ctx, d.sessionID, d.userID, d.at)
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
