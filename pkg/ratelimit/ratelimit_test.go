package ratelimit_test

import (
	"testing"
	"time"

	"github.com/cybericebox/daemon/pkg/ratelimit"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)} }

func TestWindow_LimitsAndSlides(t *testing.T) {
	clk := newClock()
	w := ratelimit.NewWindow(3, time.Minute).WithClock(clk.now)
	for i := 0; i < 3; i++ {
		if ok, _ := w.Allow("k"); !ok {
			t.Fatalf("event %d must pass", i)
		}
		clk.advance(10 * time.Second)
	}
	ok, retry := w.Allow("k")
	if ok || retry != 30*time.Second {
		t.Fatalf("4th event: ok=%v retry=%v, want denied and 30s", ok, retry)
	}
	if ok, _ = w.Allow("other"); !ok {
		t.Fatal("keys are independent")
	}
	clk.advance(30 * time.Second)
	if ok, _ = w.Allow("k"); !ok {
		t.Fatal("the oldest event left the window")
	}
}

func TestWindow_DeniedEventsAreNotCounted(t *testing.T) {
	clk := newClock()
	w := ratelimit.NewWindow(1, time.Minute).WithClock(clk.now)
	w.Allow("k")
	for i := 0; i < 50; i++ {
		clk.advance(time.Second)
		w.Allow("k") // hammering
	}
	clk.advance(10 * time.Second) // 60s after the one counted event
	if ok, _ := w.Allow("k"); !ok {
		t.Fatal("hammering must not extend the wait")
	}
}

func TestLockout_LocksAfterMaxAndDoubles(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewLockout(3, 15*time.Minute, time.Minute, 8*time.Minute).WithClock(clk.now)
	for i := 0; i < 2; i++ {
		l.Fail("a")
	}
	if d := l.Locked("a"); d != 0 {
		t.Fatalf("2 failures must not lock, got %v", d)
	}
	l.Fail("a")
	if d := l.Locked("a"); d != time.Minute {
		t.Fatalf("3rd failure locks for the base delay, got %v", d)
	}
	clk.advance(time.Minute)
	if d := l.Locked("a"); d != 0 {
		t.Fatalf("lock expired, got %v", d)
	}
	l.Fail("a") // 4th failure
	if d := l.Locked("a"); d != 2*time.Minute {
		t.Fatalf("4th failure doubles the delay, got %v", d)
	}
	for i := 0; i < 10; i++ {
		clk.advance(10 * time.Minute)
		l.Fail("a")
	}
	if d := l.Locked("a"); d > 8*time.Minute {
		t.Fatalf("delay is capped, got %v", d)
	}
}

func TestLockout_ResetAndForgiveness(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewLockout(2, 10*time.Minute, time.Minute, time.Hour).WithClock(clk.now)
	l.Fail("a")
	l.Reset("a")
	l.Fail("a")
	if l.Locked("a") != 0 {
		t.Fatal("success must clear the failures")
	}
	clk.advance(11 * time.Minute)
	l.Fail("a")
	if l.Locked("a") != 0 {
		t.Fatal("failures older than the window are forgiven")
	}
	l.Fail("a")
	if l.Locked("a") == 0 {
		t.Fatal("two fresh failures lock")
	}
}

func TestRecipient_GapAndHourlyPerKind(t *testing.T) {
	clk := newClock()
	r := ratelimit.NewRecipient(time.Minute, 3).WithClock(clk.now)
	if !r.Allow("reset", "a@b.test") {
		t.Fatal("first mail passes")
	}
	if r.Allow("reset", "a@b.test") {
		t.Fatal("a second mail inside the gap is refused")
	}
	if !r.Allow("signup", "a@b.test") {
		t.Fatal("another kind has its own quota")
	}
	clk.advance(time.Minute)
	if !r.Allow("reset", "a@b.test") {
		t.Fatal("after the gap")
	}
	clk.advance(time.Minute)
	if !r.Allow("reset", "a@b.test") {
		t.Fatal("third of the hour")
	}
	clk.advance(time.Minute)
	if r.Allow("reset", "a@b.test") {
		t.Fatal("fourth within the hour is refused")
	}
	clk.advance(time.Hour)
	if !r.Allow("reset", "a@b.test") {
		t.Fatal("quota restored after the hour")
	}
}
