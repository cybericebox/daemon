package challengeAttempt

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

// An answer is stored, listed in the journal and exported: a huge one would fill the table at the submission rate.
func TestAnAnswerLongerThanTheLimitIsRefused(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	long := strings.Repeat("a", MaxAnswerBytes+1)
	if _, err := New(id, id, id, id, long, false, time.Now()); !errors.Is(err, ErrAnswerTooLong.Err()) {
		t.Fatalf("err = %v", err)
	}
	if err := CheckAnswerLength(long); !errors.Is(err, ErrAnswerTooLong.Err()) {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(id, id, id, id, strings.Repeat("a", MaxAnswerBytes), false, time.Now()); err != nil {
		t.Fatalf("an answer at the limit: %v", err)
	}
	prev := MaxAnswerBytes
	MaxAnswerBytes = 8
	defer func() { MaxAnswerBytes = prev }()
	if CheckAnswerLength("123456789") == nil || CheckAnswerLength("12345678") != nil {
		t.Fatal("the limit is the configured one")
	}
}
