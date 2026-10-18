package errorjournalJob_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/riverqueue/river/rivertype"

	errorjournalJob "github.com/cybericebox/daemon/internal/jobs/errorjournal"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/pkg/overload"
)

type collectingReporter struct {
	mu     sync.Mutex
	events []errorJournal.Event
}

func (r *collectingReporter) Report(e errorJournal.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *collectingReporter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// A failed attempt is journaled, except when the database was busy: that is protection working under load
// (River retries the job), not a fault. The attempt that discards the job is journaled whatever the cause.
func TestHandleErrorSkipsABusyDatabaseUntilTheLastAttempt(t *testing.T) {
	reporter := &collectingReporter{}
	errorJournal.SetReporter(reporter)
	t.Cleanup(func() { errorJournal.SetReporter(nil) })

	busy := fmt.Errorf("send: %w", overload.ErrDBBusy)
	h := errorjournalJob.ErrorHandler{}
	early := &rivertype.JobRow{Kind: "k", Attempt: 1, MaxAttempts: 5}
	last := &rivertype.JobRow{Kind: "k", Attempt: 5, MaxAttempts: 5}

	if h.HandleError(context.Background(), early, busy) != nil {
		t.Fatal("the handler never changes how the job is retried")
	}
	if n := reporter.count(); n != 0 {
		t.Fatalf("a busy database was journaled: %d events", n)
	}
	h.HandleError(context.Background(), early, errors.New("boom"))
	h.HandleError(context.Background(), last, busy)
	if n := reporter.count(); n != 2 {
		t.Fatalf("want the other failure and the discard journaled, got %d events", n)
	}
}
