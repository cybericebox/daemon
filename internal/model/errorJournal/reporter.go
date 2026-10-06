package errorJournal

import "sync/atomic"

// Reporter takes an event without waiting for it. The journal use case is the reporter in a running daemon.
type Reporter interface {
	Report(Event)
}

type holder struct{ r Reporter }

var current atomic.Pointer[holder]

// SetReporter installs the process-wide reporter (once at start, like the other process-wide settings). Without
// one, Report does nothing, so code that reports can run in tests and tools unchanged.
func SetReporter(r Reporter) { current.Store(&holder{r: r}) }

// Report sends an event to the installed reporter.
func Report(e Event) {
	if h := current.Load(); h != nil && h.r != nil {
		h.r.Report(e)
	}
}
