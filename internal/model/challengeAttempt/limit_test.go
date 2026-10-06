package challengeAttempt_test

import (
	"errors"
	"testing"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
)

func ptr(v int32) *int32 { return &v }

func TestCheckAttemptLimit(t *testing.T) {
	for _, ok := range []*int32{nil, ptr(1), ptr(challengeAttempt.MaxAttemptLimit)} {
		if err := challengeAttempt.CheckAttemptLimit(ok); err != nil {
			t.Fatalf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range []int32{0, -1, challengeAttempt.MaxAttemptLimit + 1} {
		if err := challengeAttempt.CheckAttemptLimit(ptr(bad)); !errors.Is(err, challengeAttempt.ErrAttemptLimitInvalid.Err()) {
			t.Fatalf("%d: err = %v", bad, err)
		}
	}
}

func TestEffectiveAttemptLimit(t *testing.T) {
	if challengeAttempt.EffectiveAttemptLimit(nil, nil) != nil {
		t.Fatal("no value anywhere is unlimited")
	}
	if got := challengeAttempt.EffectiveAttemptLimit(ptr(5), nil); *got != 5 {
		t.Fatalf("event value = %d", *got)
	}
	if got := challengeAttempt.EffectiveAttemptLimit(ptr(5), ptr(2)); *got != 2 {
		t.Fatalf("task override lower = %d", *got)
	}
	if got := challengeAttempt.EffectiveAttemptLimit(ptr(5), ptr(9)); *got != 9 {
		t.Fatalf("task override higher = %d", *got)
	}
	if got := challengeAttempt.EffectiveAttemptLimit(nil, ptr(3)); *got != 3 {
		t.Fatalf("task alone = %d", *got)
	}
}

func TestAttemptsLeft(t *testing.T) {
	if challengeAttempt.AttemptsLeft(nil, 100) != nil || challengeAttempt.CheckAttemptsLeft(nil, 100) != nil {
		t.Fatal("unlimited never runs out")
	}
	if got := challengeAttempt.AttemptsLeft(ptr(3), 1); *got != 2 {
		t.Fatalf("left = %d", *got)
	}
	if got := challengeAttempt.AttemptsLeft(ptr(3), 7); *got != 0 {
		t.Fatalf("lowered limit under past attempts stays at 0, got %d", *got)
	}
	if err := challengeAttempt.CheckAttemptsLeft(ptr(3), 2); err != nil {
		t.Fatalf("one left: %v", err)
	}
	if err := challengeAttempt.CheckAttemptsLeft(ptr(3), 3); !errors.Is(err, challengeAttempt.ErrAttemptLimitReached.Err()) {
		t.Fatalf("none left: %v", err)
	}
}
