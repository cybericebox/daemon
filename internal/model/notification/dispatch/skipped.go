package dispatchModel

import "errors"

// SkippedMessageReserved is the journal text of a recipient on a reserved domain.
const SkippedMessageReserved = "Пропущено: зарезервований домен (RFC 2606), лист не надсилався"

// SkippedError means the message was deliberately not sent and never will be (for example the recipient is
// on a reserved domain that cannot receive mail). It is not a failure: nothing is retried or reported, and the
// journal shows the target as done with this text.
type SkippedError struct {
	Message string
}

func (e *SkippedError) Error() string { return e.Message }

// AsSkipped returns the SkippedError in err's chain, if any.
func AsSkipped(err error) (*SkippedError, bool) {
	var s *SkippedError
	if errors.As(err, &s) {
		return s, true
	}
	return nil, false
}
