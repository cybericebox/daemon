package dispatchModel

import (
	"errors"
	"time"
)

// Journal texts of a deferred delivery.
const (
	DeferredQuotaMessage = "Відкладено: вичерпано добовий ліміт"
	DeferredRateMessage  = "Відкладено: перевищено швидкість відправки"
)

// DeferredError means the message was not sent because a send limit of the SMTP
// transport was reached. It is not a failure: the job is snoozed and tries
// again after RetryAfter, and the journal shows the target as deferred.
type DeferredError struct {
	Message    string
	RetryAfter time.Duration
}

func (e *DeferredError) Error() string { return e.Message }

// AsDeferred returns the DeferredError in err's chain, if any.
func AsDeferred(err error) (*DeferredError, bool) {
	var d *DeferredError
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}
