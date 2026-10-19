package exerciseModel

import (
	"testing"
	"time"
)

func TestLengthsCappedByMax(t *testing.T) {
	got := AllowedTestLengths(TestRunLeases{Lease: time.Hour, Max: time.Hour})
	want := []time.Duration{15 * time.Minute, 30 * time.Minute, 45 * time.Minute, time.Hour}
	if len(got) != len(want) {
		t.Fatalf("lengths %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lengths %v, want %v", got, want)
		}
	}
	if m := (TestRunLeases{Lease: time.Hour, Max: 8 * time.Hour}).AllowedMinutes(); len(m) != 5 || m[4] != 120 {
		t.Fatalf("minutes %v", m)
	}
}

func TestTestLengthChoice(t *testing.T) {
	l := TestRunLeases{Lease: 3 * time.Hour, Max: 8 * time.Hour}
	if d, ok := l.TestLength(0); !ok || d != 3*time.Hour {
		t.Fatalf("zero takes the lease in force: %v %v", d, ok)
	}
	if d, ok := l.TestLength(45 * time.Minute); !ok || d != 45*time.Minute {
		t.Fatalf("an offered length: %v %v", d, ok)
	}
	if _, ok := l.TestLength(20 * time.Minute); ok {
		t.Fatal("a length that is not offered is refused")
	}
	if _, ok := (TestRunLeases{Lease: time.Hour, Max: time.Hour}).TestLength(2 * time.Hour); ok {
		t.Fatal("a length above the maximum is refused")
	}
}
