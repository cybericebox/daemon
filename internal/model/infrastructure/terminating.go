package infrastructure

import (
	"errors"
	"time"
)

// TerminatingRetryAfter is how long a caller waits before retrying a write the
// agent refused because the object with that deterministic name is still being
// deleted (asynchronous finalizers).
const TerminatingRetryAfter = 5 * time.Second

// TerminatingError means the agent did not apply a write because the target
// (LabGroup, Lab, client, policy) is still terminating. It is not a failure:
// jobs snooze or leave the work pending and retry; nothing is shown to users.
type TerminatingError struct {
	Message    string
	RetryAfter time.Duration
	Err        error
}

func (e *TerminatingError) Error() string { return e.Message }
func (e *TerminatingError) Unwrap() error { return e.Err }

// AsTerminating returns the TerminatingError in err's chain, if any.
func AsTerminating(err error) (*TerminatingError, bool) {
	return errors.AsType[*TerminatingError](err)
}
