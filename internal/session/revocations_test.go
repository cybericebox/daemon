package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

type fakeSource struct {
	mu      sync.Mutex
	now     time.Time
	rows    []Revocation
	err     error
	since   []time.Time
	deleted int
}

func (f *fakeSource) DatabaseTime(context.Context) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now, f.err
}

func (f *fakeSource) ActiveRevocations(context.Context) ([]Revocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Revocation
	for _, r := range f.rows {
		if r.ExpiresAt.After(f.now) {
			out = append(out, r)
		}
	}
	return out, f.err
}

func (f *fakeSource) RevocationsSince(_ context.Context, since time.Time) ([]Revocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = append(f.since, since)
	var out []Revocation
	for _, r := range f.rows {
		if r.RevokedAt.After(since) {
			out = append(out, r)
		}
	}
	return out, f.err
}

func (f *fakeSource) DeleteExpiredRevocations(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted++
	return 0, nil
}

func (f *fakeSource) add(id uuid.UUID, revokedAt, expires time.Time) {
	f.mu.Lock()
	f.rows = append(f.rows, Revocation{SessionID: id, UserID: uuid.Must(uuid.NewV7()), RevokedAt: revokedAt, ExpiresAt: expires})
	f.mu.Unlock()
}

func newRevocations(src *fakeSource, now *time.Time) *Revocations {
	r := NewRevocations(src, DefaultRevocationConfig(30*time.Second))
	r.now = func() time.Time { return *now }
	return r
}

func TestRevocationsAreNotHealthyBeforeTheFirstLoad(t *testing.T) {
	now := t0
	r := newRevocations(&fakeSource{now: now}, &now)
	if r.Healthy() {
		t.Fatal("a replica that has not loaded the list must refuse signed-in requests")
	}
}

func TestLoadTakesTheRowsNotYetExpired(t *testing.T) {
	now := t0
	src := &fakeSource{now: now}
	live, dead := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	src.add(live, now.Add(-time.Minute), now.Add(time.Hour))
	src.add(dead, now.Add(-time.Hour), now.Add(-time.Minute))
	r := newRevocations(src, &now)
	if err := r.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Revoked(live) || r.Revoked(dead) || !r.Healthy() {
		t.Fatalf("live %v dead %v healthy %v", r.Revoked(live), r.Revoked(dead), r.Healthy())
	}
}

func TestPollReachesBackByTheOverlapAndDedupes(t *testing.T) {
	now := t0
	src := &fakeSource{now: now}
	r := newRevocations(src, &now)
	if err := r.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A transaction that began 5 s ago and commits now: its revoked_at is older than the watermark.
	late := uuid.Must(uuid.NewV7())
	now = now.Add(2 * time.Second)
	src.now = now
	src.add(late, now.Add(-5*time.Second), now.Add(time.Hour))
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Revoked(late) {
		t.Fatal("the overlap must catch a late commit")
	}
	if got, want := src.since[0], t0.Add(-10*time.Second); !got.Equal(want) {
		t.Fatalf("since %v, want watermark - 10 s = %v", got, want)
	}
	// The next poll reads the same row again; the session id dedupes.
	now = now.Add(time.Second)
	src.now = now
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.ids) != 1 {
		t.Fatalf("one session, got %d entries", len(r.ids))
	}
}

func TestPollDropsExpiredIDs(t *testing.T) {
	now := t0
	src := &fakeSource{now: now}
	r := newRevocations(src, &now)
	_ = r.Load(context.Background())
	id := uuid.Must(uuid.NewV7())
	r.Add(id, now.Add(time.Minute))
	now = now.Add(2 * time.Minute)
	src.now = now
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Revoked(id) {
		t.Fatal("an expired revocation must leave memory")
	}
}

func TestFailsClosedWhenThePollIsStaleAndRecovers(t *testing.T) {
	now := t0
	src := &fakeSource{now: now}
	r := newRevocations(src, &now)
	_ = r.Load(context.Background())
	src.err = errors.New("db down")
	now = now.Add(29 * time.Second)
	if err := r.Poll(context.Background()); err == nil || !r.Healthy() {
		t.Fatalf("within the limit the list is still trusted: err %v healthy %v", err, r.Healthy())
	}
	now = now.Add(2 * time.Second)
	if r.Healthy() {
		t.Fatal("past the limit the replica must fail closed")
	}
	src.err = nil
	src.now = now
	if err := r.Poll(context.Background()); err != nil || !r.Healthy() {
		t.Fatalf("a good poll recovers: err %v healthy %v", err, r.Healthy())
	}
}

func TestAddRevokesAtOnceOnThisReplica(t *testing.T) {
	now := t0
	r := newRevocations(&fakeSource{now: now}, &now)
	id := uuid.Must(uuid.NewV7())
	r.Add(id, now.Add(time.Hour))
	if !r.Revoked(id) {
		t.Fatal("a revocation written here must apply here without waiting for the poll")
	}
}
