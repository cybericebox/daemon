package participantPreviewModel

import (
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

func TestRunCheckOwnStateExpiryAndFirstSolve(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	task := uuid.Must(uuid.NewV7())
	run := NewRun(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), now)
	run.Flags[task.String()] = "ICE{secret}"
	if correct, first := run.Check(task, "wrong", now); correct || first {
		t.Fatal("wrong flag accepted")
	}
	if correct, first := run.Check(task, "ICE{secret}", now); !correct || !first {
		t.Fatal("valid flag rejected")
	}
	if _, first := run.Check(task, "ICE{secret}", now); first {
		t.Fatal("duplicate first solve")
	}
	if correct, _ := run.Check(task, "ICE{secret}", run.ExpiresAt); correct {
		t.Fatal("expired flag accepted")
	}
	other := NewRun(run.EventID, uuid.Must(uuid.NewV7()), run.SelectionID, now)
	if correct, _ := other.Check(task, "ICE{secret}", now); correct {
		t.Fatal("state leaked")
	}
}
func TestRunHintsAreIdempotent(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	run := NewRun(uuid.Nil, uuid.Nil, uuid.Nil, now)
	task, hint := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	run.UnlockHint(task, hint, now)
	run.UnlockHint(task, hint, now.Add(time.Minute))
	if got := run.Hints[task.String()][hint.String()]; !got.Equal(now) {
		t.Fatal("hint timestamp changed")
	}
}

func TestRunCheckTrimsSurroundingWhitespace(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	task := uuid.Must(uuid.NewV7())
	run := NewRun(uuid.Nil, uuid.Nil, uuid.Nil, now)
	run.Flags[task.String()] = "ICE{secret}"
	if correct, _ := run.Check(task, " ICE{secret}\n", now); !correct {
		t.Fatal("whitespace around a valid flag rejected")
	}
	if correct, _ := run.Check(task, "ice{secret}", now); correct {
		t.Fatal("case mismatch accepted")
	}
}
