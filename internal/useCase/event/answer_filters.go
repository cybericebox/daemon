package event

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// AnswerFilterOp is how a moderation list filter compares a form answer.
type AnswerFilterOp string

const (
	// AnswerFilterContains keeps rows whose text answer contains Value.
	AnswerFilterContains AnswerFilterOp = "contains"
	// AnswerFilterAny keeps rows whose choice answer holds one of Values.
	AnswerFilterAny AnswerFilterOp = "any"
	// AnswerFilterBool keeps rows whose checkbox answer equals Value.
	AnswerFilterBool AnswerFilterOp = "bool"
	// AnswerFilterPresent keeps rows that did (Value true) or did not answer.
	AnswerFilterPresent AnswerFilterOp = "present"
	// AnswerFilterRange keeps rows whose number or date is within From..To.
	AnswerFilterRange AnswerFilterOp = "range"
)

// Range filter types: numbers, dates (RFC3339 or "YYYY-MM-DD" bounds) and
// times of day ("HH:MM" bounds).
const (
	rangeNumber = "number"
	rangeDate   = "date"
	rangeTime   = "time"
)

var timeOfDay = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

const (
	maxAnswerFilters      = 20
	maxAnswerFilterValues = 50
	maxAnswerFilterText   = 100
	maxAnswerFilterKey    = 128
)

var errAnswerFiltersInvalid = errors.New("invalid answer filters")

// ErrListQueryInvalid rejects an unknown column in a table filter or sort.
var ErrListQueryInvalid = errors.New("invalid list filter or sort")

// AnswerFilter narrows a moderation list by one form answer or, in table
// mode, one column ("@"-prefixed key). It is matched in SQL by
// event_answer_matches (migrations 0098, 0104, 0105 and 0106).
type AnswerFilter struct {
	Key    string         `json:"key"`
	Op     AnswerFilterOp `json:"op"`
	Value  any            `json:"value,omitempty"`
	Values []string       `json:"values,omitempty"`
	Type   string         `json:"type,omitempty"`
	From   any            `json:"from,omitempty"`
	To     any            `json:"to,omitempty"`
	// FromExclusive/ToExclusive turn a number bound into > or <.
	FromExclusive bool `json:"fromExclusive,omitempty"`
	ToExclusive   bool `json:"toExclusive,omitempty"`
}

type answerFilterInput struct {
	Key    string
	Op     AnswerFilterOp
	Value  json.RawMessage
	Values []string
	Type   string
	From   json.RawMessage
	To     json.RawMessage
	// Number ranges only.
	FromExclusive bool
	ToExclusive   bool
}

// rangeBound reads one optional bound of a range filter.
func rangeBound(raw json.RawMessage, kind string) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	switch kind {
	case rangeNumber:
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil {
			return nil, false
		}
		return number, true
	case rangeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, false
		}
		if day, err := time.Parse(time.DateOnly, text); err == nil {
			return day.Format(time.DateOnly), true
		}
		at, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return nil, false
		}
		return at.UTC().Format(time.RFC3339), true
	case rangeTime:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || !timeOfDay.MatchString(text) {
			return nil, false
		}
		return text, true
	}
	return nil, false
}

// ParseAnswerFilters reads the `filters` query value: a JSON array of
// {Key, Op, Value, Values}. Empty input means no filters.
func ParseAnswerFilters(raw string) ([]AnswerFilter, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var inputs []answerFilterInput
	if err := json.Unmarshal([]byte(raw), &inputs); err != nil || len(inputs) > maxAnswerFilters {
		return nil, errAnswerFiltersInvalid
	}
	out := make([]AnswerFilter, 0, len(inputs))
	for _, in := range inputs {
		key := strings.TrimSpace(in.Key)
		if key == "" || len(key) > maxAnswerFilterKey {
			return nil, errAnswerFiltersInvalid
		}
		filter := AnswerFilter{Key: key, Op: in.Op}
		switch in.Op {
		case AnswerFilterContains:
			var text string
			if err := json.Unmarshal(in.Value, &text); err != nil {
				return nil, errAnswerFiltersInvalid
			}
			text = strings.TrimSpace(text)
			if text == "" || utf8.RuneCountInString(text) > maxAnswerFilterText {
				return nil, errAnswerFiltersInvalid
			}
			filter.Value = text
		case AnswerFilterAny:
			if len(in.Values) == 0 || len(in.Values) > maxAnswerFilterValues {
				return nil, errAnswerFiltersInvalid
			}
			for _, value := range in.Values {
				if utf8.RuneCountInString(value) > 2*maxAnswerFilterText {
					return nil, errAnswerFiltersInvalid
				}
			}
			filter.Values = in.Values
		case AnswerFilterRange:
			if in.Type != rangeNumber && in.Type != rangeDate && in.Type != rangeTime {
				return nil, errAnswerFiltersInvalid
			}
			if in.Type != rangeNumber && (in.FromExclusive || in.ToExclusive) {
				return nil, errAnswerFiltersInvalid
			}
			from, okFrom := rangeBound(in.From, in.Type)
			to, okTo := rangeBound(in.To, in.Type)
			if !okFrom || !okTo || (from == nil && to == nil) {
				return nil, errAnswerFiltersInvalid
			}
			filter.Type, filter.From, filter.To = in.Type, from, to
			filter.FromExclusive, filter.ToExclusive = in.FromExclusive && from != nil, in.ToExclusive && to != nil
		case AnswerFilterBool, AnswerFilterPresent:
			var flag bool
			if err := json.Unmarshal(in.Value, &flag); err != nil {
				return nil, errAnswerFiltersInvalid
			}
			filter.Value = flag
		default:
			return nil, errAnswerFiltersInvalid
		}
		out = append(out, filter)
	}
	return out, nil
}

// answerFiltersJSON is the SQL argument: always a JSON array.
func answerFiltersJSON(filters []AnswerFilter) []byte {
	if len(filters) == 0 {
		return []byte("[]")
	}
	raw, err := json.Marshal(filters)
	if err != nil {
		return []byte("[]")
	}
	return raw
}
