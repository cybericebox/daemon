package challengeAttempt

// MaxAttemptLimit is the largest "max flag attempts" an organizer can set.
const MaxAttemptLimit int32 = 1000

// CheckAttemptLimit validates an optional limit: nil means "no value" (unlimited on an event, the event's value on
// a task), otherwise a whole number from 1 to MaxAttemptLimit.
func CheckAttemptLimit(limit *int32) error {
	if limit != nil && (*limit < 1 || *limit > MaxAttemptLimit) {
		return ErrAttemptLimitInvalid.Err()
	}
	return nil
}

// EffectiveAttemptLimit resolves the limit of one task: the task's own value, else the event's, else unlimited (nil).
func EffectiveAttemptLimit(event, task *int32) *int32 {
	if task != nil {
		return task
	}
	return event
}

// AttemptsLeft is how many wrong attempts remain; nil when there is no limit.
func AttemptsLeft(limit *int32, wrong int64) *int32 {
	if limit == nil {
		return nil
	}
	left := int64(*limit) - wrong
	if left < 0 {
		left = 0
	}
	out := int32(left)
	return &out
}

// CheckAttemptsLeft refuses a submission once no attempts are left.
func CheckAttemptsLeft(limit *int32, wrong int64) error {
	if left := AttemptsLeft(limit, wrong); left != nil && *left == 0 {
		return ErrAttemptLimitReached.Err()
	}
	return nil
}
