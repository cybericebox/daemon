package errorJournalUseCase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
)

// memBus stands in for Postgres NOTIFY: every listener hears every message, the sender too.
type memBus struct {
	mu        sync.Mutex
	listeners []func(RelayMessage)
}

type memRelay struct{ bus *memBus }

func (r memRelay) Publish(_ context.Context, m RelayMessage) error {
	r.bus.mu.Lock()
	listeners := append([]func(RelayMessage){}, r.bus.listeners...)
	r.bus.mu.Unlock()
	for _, l := range listeners {
		l(m)
	}
	return nil
}

func (r memRelay) Listen(ctx context.Context, handle func(RelayMessage)) {
	r.bus.mu.Lock()
	r.bus.listeners = append(r.bus.listeners, handle)
	r.bus.mu.Unlock()
	<-ctx.Done()
}

// secondReplica is another journal over the same repository (the shared database) and the same channels.
func secondReplica(h *harness, bus *memBus) *Journal {
	j := New(
		Dependencies{
			Repo:     h.repo,
			Telegram: h.tg,
			Mailer:   h.mail,
			Relay:    memRelay{bus},
			Config:   DefaultConfig(),
		},
	)
	j.now = func() time.Time { return h.now }
	return j
}

func TestARecordedErrorReachesTheStreamOfTheOtherReplica(t *testing.T) {
	h := newHarness(DefaultConfig())
	bus := &memBus{}
	h.j.relay = memRelay{bus}
	other := secondReplica(h, bus)
	lctx, stop := context.WithCancel(context.Background())
	defer stop()
	go h.j.Listen(lctx)
	go other.Listen(lctx)
	require.Eventually(
		t,
		func() bool { bus.mu.Lock(); defer bus.mu.Unlock(); return len(bus.listeners) == 2 },
		time.Second,
		time.Millisecond,
	)

	here, endHere := h.j.ErrorJournalStream()
	defer endHere()
	there, endThere := other.ErrorJournalStream()
	defer endThere()

	rec, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)

	select {
	case e := <-there:
		assert.Equal(t, rec.Group.ID, e.Group.ID)
		assert.True(t, e.New)
		require.NotNil(t, e.Sample, "the sample is read from the database on the receiving replica")
		assert.Equal(t, "boom", e.Sample.Message)
	case <-time.After(time.Second):
		t.Fatal("the other replica's stream never saw the error")
	}
	select {
	case e := <-here:
		assert.Equal(t, rec.Group.ID, e.Group.ID)
	case <-time.After(time.Second):
		t.Fatal("the own stream never saw the error")
	}
	// The recording replica shows it once: its own relay message is ignored.
	select {
	case <-here:
		t.Fatal("the own stream got the event twice")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAGroupStatusChangeReachesTheOtherReplica(t *testing.T) {
	h := newHarness(DefaultConfig())
	bus := &memBus{}
	h.j.relay = memRelay{bus}
	other := secondReplica(h, bus)
	lctx, stop := context.WithCancel(context.Background())
	defer stop()
	go h.j.Listen(lctx)
	go other.Listen(lctx)
	require.Eventually(
		t,
		func() bool { bus.mu.Lock(); defer bus.mu.Unlock(); return len(bus.listeners) == 2 },
		time.Second,
		time.Millisecond,
	)
	rec, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	there, end := other.ErrorJournalStream()
	defer end()

	_, err = h.j.SetErrorGroupStatus(ctx, rec.Group.ID, errorJournal.StatusResolved)
	require.NoError(t, err)
	select {
	case e := <-there:
		assert.Equal(t, errorJournal.StatusResolved, e.Group.Status)
		assert.Nil(t, e.Sample)
	case <-time.After(time.Second):
		t.Fatal("the status change never reached the other replica")
	}
}

func TestTwoReplicasSendOneMessageForTheSameError(t *testing.T) {
	h := newHarness(DefaultConfig())
	other := secondReplica(h, &memBus{})
	_, err := h.j.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	_, err = other.Record(ctx, fiveXX("boom"))
	require.NoError(t, err)
	assert.Equal(t, 1, h.tg.count("100"), "the message is claimed in the database, not per replica")
	assert.Len(t, h.mail.sent, 1)
}

func TestTwoReplicasSendOneMessagePerCooldownForANoisyKind(t *testing.T) {
	h := newHarness(DefaultConfig())
	other := secondReplica(h, &memBus{})
	forbidden := func(route string) errorJournal.Event {
		return errorJournal.Event{
			Kind:       errorJournal.KindHTTP403,
			Source:     route,
			Route:      route,
			Method:     "GET",
			HTTPStatus: 403,
			Message:    "forbidden",
			Notify:     errorJournal.NotifyAlways,
		}
	}
	_, err := h.j.Record(ctx, forbidden("/api/a"))
	require.NoError(t, err)
	_, err = other.Record(ctx, forbidden("/api/b"))
	require.NoError(t, err)
	assert.Equal(
		t,
		1,
		h.tg.count("100"),
		"one message per cooldown for the kind, whichever replica saw it",
	)

	h.now = h.now.Add(h.j.cfg.NotifyCooldown + time.Second)
	_, err = other.Record(ctx, forbidden("/api/c"))
	require.NoError(t, err)
	assert.Equal(t, 2, h.tg.count("100"))
}
