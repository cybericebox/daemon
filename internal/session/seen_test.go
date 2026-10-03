package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

type seenWrite struct {
	sessionID uuid.UUID
	at        time.Time
}

type fakeWriter struct {
	mu     sync.Mutex
	writes []seenWrite
}

func (f *fakeWriter) WriteSeen(_ context.Context, sessionID, _ uuid.UUID, at time.Time) error {
	f.mu.Lock()
	f.writes = append(f.writes, seenWrite{sessionID, at})
	f.mu.Unlock()
	return nil
}

func (f *fakeWriter) list() []seenWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenWrite(nil), f.writes...)
}

func newSeen(w *fakeWriter, now *time.Time) *Seen {
	s := NewSeen(w, SeenWindow)
	s.now = func() time.Time { return *now }
	return s
}

func TestFirstRequestOfAWindowWritesAtOnceTheRestOnlyUpdateMemory(t *testing.T) {
	now, w := t0, &fakeWriter{}
	s := newSeen(w, &now)
	sid, uid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	s.Touch(sid, uid)
	s.wg.Wait()
	for i := 0; i < 5; i++ {
		now = now.Add(5 * time.Second)
		s.Touch(sid, uid)
	}
	s.wg.Wait()
	if got := w.list(); len(got) != 1 || !got[0].at.Equal(t0) {
		t.Fatalf("one write at the first request, got %+v", got)
	}
}

func TestEndOfWindowWritesTheLatestTimeOnlyIfThereWasOne(t *testing.T) {
	now, w := t0, &fakeWriter{}
	s := newSeen(w, &now)
	quiet, busy := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	s.Touch(quiet, uuid.Nil)
	s.Touch(busy, uuid.Nil)
	now = now.Add(10 * time.Second)
	s.Touch(busy, uuid.Nil)
	now = now.Add(5 * time.Second)
	last := now
	s.Touch(busy, uuid.Nil)
	s.wg.Wait()
	now = t0.Add(SeenWindow)
	s.Sweep()
	s.wg.Wait()
	var busyWrites []time.Time
	for _, wr := range w.list() {
		if wr.sessionID == quiet {
			if !wr.at.Equal(t0) {
				t.Fatalf("a session with no later request must not be written again: %+v", wr)
			}
		} else {
			busyWrites = append(busyWrites, wr.at)
		}
	}
	if len(busyWrites) != 2 || !busyWrites[0].Equal(t0) || !busyWrites[1].Equal(last) {
		t.Fatalf("busy session: first and latest time, got %v", busyWrites)
	}
	// The window is closed: the next request opens a new one and writes at once.
	now = now.Add(time.Second)
	s.Touch(busy, uuid.Nil)
	s.wg.Wait()
	if n := len(w.list()); n != 4 {
		t.Fatalf("a new window writes at once, writes %d", n)
	}
}

func TestFlushWritesWhatIsPendingOnShutdown(t *testing.T) {
	now, w := t0, &fakeWriter{}
	s := newSeen(w, &now)
	sid := uuid.Must(uuid.NewV7())
	s.Touch(sid, uuid.Nil)
	s.wg.Wait()
	now = now.Add(7 * time.Second)
	s.Touch(sid, uuid.Nil)
	s.Flush(context.Background())
	got := w.list()
	if len(got) != 2 || !got[1].at.Equal(t0.Add(7*time.Second)) {
		t.Fatalf("flush must write the pending time, got %+v", got)
	}
}
