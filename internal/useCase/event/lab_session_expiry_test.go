package event

import (
	"testing"
	"time"
)

func TestLabSessionExpiryAt(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	finish := now.Add(48 * time.Hour)
	if got := labSessionExpiryAt(&finish, now); !got.Equal(finish.Add(labSessionFinishBuffer)) {
		t.Fatalf("token must last until the finish plus the buffer, got %s", got)
	}
	if got := labSessionExpiryAt(nil, now); !got.IsZero() {
		t.Fatalf("an event without a finish uses the fallback ttl, got %s", got)
	}
	past := now.Add(-2 * time.Hour)
	if got := labSessionExpiryAt(&past, now); !got.IsZero() {
		t.Fatalf("a finished event uses the fallback ttl, got %s", got)
	}
}
