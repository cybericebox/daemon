package leader_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/leader"
)

// sharedLock is one lock key that several fake replicas race for.
type sharedLock struct {
	mu     sync.Mutex
	owner  *fakeHeld
	nextID int
}

type fakeHeld struct {
	s     *sharedLock
	id    int
	dead  atomic.Bool
	freed atomic.Bool
}

func (s *sharedLock) replica() leader.Lock { return replicaLock{s} }

type replicaLock struct{ s *sharedLock }

func (r replicaLock) TryLock(context.Context) (leader.Held, bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.owner != nil && !r.s.owner.freed.Load() {
		return nil, false, nil
	}
	r.s.nextID++
	r.s.owner = &fakeHeld{s: r.s, id: r.s.nextID}
	return r.s.owner, true, nil
}

func (h *fakeHeld) Alive(context.Context) error {
	if h.dead.Load() {
		return errors.New("session lost")
	}
	return nil
}

func (h *fakeHeld) Release() { h.freed.Store(true) }

// loops stands in for the leader loops of one replica.
type loops struct {
	running atomic.Int32
	starts  atomic.Int32
}

func (l *loops) run(ctx context.Context) {
	l.starts.Add(1)
	l.running.Add(1)
	defer l.running.Add(-1)
	<-ctx.Done()
}

func fast() leader.Config {
	return leader.Config{Name: "test", Retry: 5 * time.Millisecond, Check: 5 * time.Millisecond}
}

func TestOnlyOneReplicaRunsTheLeaderLoops(t *testing.T) {
	lock := &sharedLock{}
	a, b := &loops{}, &loops{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go leader.New(lock.replica(), fast()).Run(ctx, a.run)
	go leader.New(lock.replica(), fast()).Run(ctx, b.run)

	require.Eventually(
		t,
		func() bool { return a.running.Load()+b.running.Load() == 1 },
		time.Second,
		time.Millisecond,
	)
	// Stays at one while both keep retrying.
	require.Never(
		t,
		func() bool { return a.running.Load()+b.running.Load() != 1 },
		100*time.Millisecond,
		time.Millisecond,
	)
}

func TestLeadershipMovesWhenTheLeaderStops(t *testing.T) {
	lock := &sharedLock{}
	a, b := &loops{}, &loops{}
	ctxA, stopA := context.WithCancel(context.Background())
	ctxB, stopB := context.WithCancel(context.Background())
	defer stopB()
	doneA := make(chan struct{})
	go func() { defer close(doneA); leader.New(lock.replica(), fast()).Run(ctxA, a.run) }()
	require.Eventually(
		t,
		func() bool { return a.running.Load() == 1 },
		time.Second,
		time.Millisecond,
	)
	go leader.New(lock.replica(), fast()).Run(ctxB, b.run)
	require.Never(
		t,
		func() bool { return b.running.Load() != 0 },
		50*time.Millisecond,
		time.Millisecond,
	)

	stopA() // the leader shuts down: loops stop, the lock is released
	<-doneA
	require.Zero(t, a.running.Load())
	require.Eventually(
		t,
		func() bool { return b.running.Load() == 1 },
		time.Second,
		time.Millisecond,
	)
}

func TestLosingTheSessionStopsTheLoopsAndHandsOver(t *testing.T) {
	lock := &sharedLock{}
	a, b := &loops{}, &loops{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go leader.New(lock.replica(), fast()).Run(ctx, a.run)
	require.Eventually(
		t,
		func() bool { return a.running.Load() == 1 },
		time.Second,
		time.Millisecond,
	)
	go leader.New(lock.replica(), fast()).Run(ctx, b.run)

	lock.mu.Lock()
	first := lock.owner
	lock.mu.Unlock()
	first.dead.Store(true) // connection dropped: the server frees the lock with the session
	first.freed.Store(true)

	// The old leader stops its loops; one of the replicas leads again (the freed lock may go to either).
	require.Eventually(
		t,
		func() bool { return a.running.Load()+b.running.Load() == 1 && a.starts.Load()+b.starts.Load() >= 2 },
		time.Second,
		time.Millisecond,
	)
}

// slowLock hands out a session whose check reports the time it was given to answer.
type slowLock struct {
	given chan time.Duration
}

type slowHeld struct{ given chan time.Duration }

func (l slowLock) TryLock(context.Context) (leader.Held, bool, error) {
	return slowHeld(l), true, nil
}

func (h slowHeld) Alive(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("the check has no deadline")
	}
	select {
	case h.given <- time.Until(deadline):
	default:
	}
	return nil
}

func (slowHeld) Release() {}

// The time a check may take is its own setting: a short Check interval must not shorten it, or a slow answer on a
// busy host would look like a lost session and hand the leadership away while the lock is still held.
func TestTheCheckTimeoutDoesNotFollowTheCheckInterval(t *testing.T) {
	lock := slowLock{given: make(chan time.Duration, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := leader.Config{Name: "test", Retry: time.Hour, Check: time.Millisecond, AliveTimeout: time.Hour}
	l := &loops{}
	go leader.New(lock, cfg).Run(ctx, l.run)

	select {
	case given := <-lock.given:
		require.Greater(t, given, time.Minute, "a 1ms interval left the check only %s to answer", given)
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never checked its session")
	}
	require.Eventually(t, func() bool { return l.running.Load() == 1 }, time.Second, time.Millisecond,
		"the leader keeps leading while the check answers")
}

func TestTheCheckTimeoutDefaultsToFiveSeconds(t *testing.T) {
	lock := slowLock{given: make(chan time.Duration, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &loops{}
	go leader.New(lock, leader.Config{Name: "test", Retry: time.Hour, Check: time.Millisecond}).Run(ctx, l.run)

	select {
	case given := <-lock.given:
		require.Greater(t, given, 4*time.Second)
		require.LessOrEqual(t, given, 5*time.Second)
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never checked its session")
	}
}
