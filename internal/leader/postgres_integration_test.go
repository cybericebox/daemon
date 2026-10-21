package leader_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/advisory"
	"github.com/cybericebox/daemon/internal/leader"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Two "replicas" with pools of their own race for the real advisory lock.
func TestPostgresLockElectsOneReplicaAndHandsOver(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	key := advisory.Key("leader", t.Name())
	var runningA, runningB atomic.Int32
	loop := func(n *atomic.Int32) func(context.Context) {
		return func(ctx context.Context) { n.Add(1); defer n.Add(-1); <-ctx.Done() }
	}
	cfg := leader.Config{Name: "test", Retry: 20 * time.Millisecond, Check: 20 * time.Millisecond}
	ctxA, stopA := context.WithCancel(context.Background())
	ctxB, stopB := context.WithCancel(context.Background())
	doneB := make(chan struct{})
	defer func() { stopB(); <-doneB }() // before the pool is closed
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		leader.New(leader.PostgresLock{Pool: db.Pool, Key: key}, cfg).Run(ctxA, loop(&runningA))
	}()
	require.Eventually(
		t,
		func() bool { return runningA.Load() == 1 },
		5*time.Second,
		10*time.Millisecond,
	)
	go func() {
		defer close(doneB)
		leader.New(leader.PostgresLock{Pool: db.Pool, Key: key}, cfg).Run(ctxB, loop(&runningB))
	}()
	require.Never(
		t,
		func() bool { return runningB.Load() != 0 },
		300*time.Millisecond,
		10*time.Millisecond,
	)

	stopA()
	<-doneA
	require.Eventually(
		t,
		func() bool { return runningB.Load() == 1 },
		5*time.Second,
		10*time.Millisecond,
	)
}

func TestPostgresLockIsLostWhenTheSessionDies(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	key := advisory.Key("leader", t.Name())
	ctx := context.Background()
	held, ok, err := advisory.TryLock(ctx, db.Pool, key)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = advisory.TryLock(ctx, db.Pool, key)
	require.NoError(t, err)
	require.False(t, ok)
	held.Release() // the session ends: the lock goes with it
	again, ok, err := advisory.TryLock(ctx, db.Pool, key)
	require.NoError(t, err)
	require.True(t, ok)
	again.Release()
}
