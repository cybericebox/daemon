package errorJournalUseCase

import (
	"sync"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
)

// StreamEvent is what the live stream sends: the group after the change and, for a new occurrence, its sample.
type StreamEvent struct {
	Group  errorJournal.Group
	Sample *errorJournal.Sample
	// New: the fingerprint was first seen now, or came back after it was resolved.
	New bool
}

// Hub fans events out to the open streams. A slow stream loses events instead of holding the writer back; the
// page refetches on reconnect anyway.
type Hub struct {
	mu   sync.Mutex
	subs map[chan StreamEvent]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan StreamEvent]struct{}{}} }

// Subscribe returns a channel of events and the function that ends the subscription.
func (h *Hub) Subscribe() (<-chan StreamEvent, func()) {
	ch := make(chan StreamEvent, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

// Publish sends e to every stream without blocking.
func (h *Hub) Publish(e StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
