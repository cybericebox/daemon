package advisory_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/advisory"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestLockWaitsForTheHolderAndGivesUpWithTheContext(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	key := advisory.Key("test", "wait")
	first, err := advisory.Lock(context.Background(), db.Pool, key)
	require.NoError(t, err)

	short, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = advisory.Lock(short, db.Pool, key)
	require.Error(t, err, "the second caller must not get the lock while it is held")

	got := make(chan *advisory.Held, 1)
	go func() {
		h, lockErr := advisory.Lock(context.Background(), db.Pool, key)
		if lockErr == nil {
			got <- h
		}
	}()
	select {
	case <-got:
		t.Fatal("the lock was given while held")
	case <-time.After(200 * time.Millisecond):
	}
	first.Release()
	select {
	case h := <-got:
		h.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting caller never got the lock after the release")
	}
}

func TestKeysDifferByNamespaceAndID(t *testing.T) {
	require.NotEqual(t, advisory.Key("a", "1"), advisory.Key("a", "2"))
	require.NotEqual(t, advisory.Key("a", "1"), advisory.Key("b", "1"))
}

// A process holds at most MaxHeld lock sessions: they take connections out of the pool for good, so the next lock
// waits (and gives up with its context) instead of opening one more connection.
func TestLockSessionsAreBounded(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	var held []*advisory.Held
	for i := range advisory.MaxHeld {
		h, err := advisory.Lock(context.Background(), db.Pool, advisory.Key("test-bound", string(rune('a'+i))))
		require.NoError(t, err)
		held = append(held, h)
	}
	require.Equal(t, advisory.MaxHeld, advisory.HeldCount())

	short, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := advisory.Lock(short, db.Pool, advisory.Key("test-bound", "extra"))
	require.Error(t, err, "no session beyond the bound")
	require.Equal(t, advisory.MaxHeld, advisory.HeldCount())

	held[0].Release()
	held[0].Release() // a second release frees nothing more
	require.Equal(t, advisory.MaxHeld-1, advisory.HeldCount())
	h, err := advisory.Lock(context.Background(), db.Pool, advisory.Key("test-bound", "extra"))
	require.NoError(t, err, "a freed session lets the next one in")
	h.Release()
	for _, other := range held[1:] {
		other.Release()
	}
	require.Zero(t, advisory.HeldCount())
}
