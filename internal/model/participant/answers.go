package participantModel

import (
	"reflect"
	"time"
)

// MergeEditableAnswers applies a participant's own edit of the registration
// answers. editable maps every form field key to its editable flag. Only
// editable keys may change; a non-editable or unknown key is accepted only
// when its value equals the stored one (an absent stored key equals null).
// Omitted keys keep their stored values. stored is never mutated.
func MergeEditableAnswers(editable map[string]bool, stored, submitted map[string]any) (map[string]any, error) {
	merged := make(map[string]any, len(stored)+len(submitted))
	for key, value := range stored {
		merged[key] = value
	}
	for key, value := range submitted {
		if editable[key] {
			merged[key] = value
			continue
		}
		if !reflect.DeepEqual(stored[key], value) {
			return nil, ErrParticipantFieldNotEditable.Err()
		}
	}
	return merged, nil
}

// AnswersEditable reports whether a participant may still edit their own
// answers: until the event's effective finish (nil means not finished).
func AnswersEditable(effectiveFinishAt *time.Time, now time.Time) bool {
	return effectiveFinishAt == nil || now.Before(*effectiveFinishAt)
}

// LockFilledAnswers applies a participant's first submission of the form on
// top of answers an organizer may have prefilled: a non-editable field that
// already has a value keeps it, and submitting a different value is refused.
// Fields without a stored value (or editable ones) take the submitted value.
// stored and submitted are never mutated.
func LockFilledAnswers(editable map[string]bool, stored, submitted map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(submitted))
	for key, value := range submitted {
		out[key] = value
	}
	for key, value := range stored {
		if editable[key] || isEmptyAnswer(value) {
			continue
		}
		if sent, ok := submitted[key]; ok && !reflect.DeepEqual(sent, value) {
			return nil, ErrParticipantFieldPrefilled.Err()
		}
		out[key] = value
	}
	return out, nil
}

func isEmptyAnswer(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []any:
		return len(typed) == 0
	}
	return false
}
