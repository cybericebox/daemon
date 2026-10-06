package eventFormModel

import (
	"fmt"
	"strings"
	"time"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// InputDate is the answer type of a «Дата / час» question. DateMode says what
// the participant enters: a date ("YYYY-MM-DD"), a time of day ("HH:MM",
// local, no zone) or both (a UTC ISO datetime, "2026-09-29T12:30:00Z").
// An empty mode is a date, as saved before times existed.
const (
	InputDate        = "date"
	DateModeDate     = "date"
	DateModeTime     = "time"
	DateModeDateTime = "datetime"
)

// Condition operators a date question adds to equals / not_equals.
const (
	OperatorBefore = "before"
	OperatorAfter  = "after"
)

func dateMode(block eventContentModel.Block) string {
	if block.DateMode == DateModeDateTime || block.DateMode == DateModeTime {
		return block.DateMode
	}
	return DateModeDate
}

// ParseDateAnswer reads a date answer of the given mode; datetimes must be UTC
// and times are compared as times of day.
func ParseDateAnswer(mode, value string) (time.Time, bool) {
	if mode == DateModeTime {
		if len(value) != 5 {
			return time.Time{}, false
		}
		at, err := time.Parse("15:04", value)
		return at, err == nil
	}
	if mode == DateModeDateTime {
		if !strings.HasSuffix(value, "Z") {
			return time.Time{}, false
		}
		at, err := time.Parse(time.RFC3339Nano, value)
		return at, err == nil
	}
	day, err := time.Parse(time.DateOnly, value)
	return day, err == nil
}

func dateValue(block eventContentModel.Block, value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	return ParseDateAnswer(dateMode(block), text)
}

func validateDateQuestion(block eventContentModel.Block) error {
	if block.DateMode != "" && block.DateMode != DateModeDate && block.DateMode != DateModeTime && block.DateMode != DateModeDateTime {
		return fmt.Errorf("field %q must take a date, a time or both", block.Key)
	}
	var bounds [2]time.Time
	for i, bound := range []string{block.MinDate, block.MaxDate} {
		if bound == "" {
			continue
		}
		at, ok := ParseDateAnswer(dateMode(block), bound)
		if !ok {
			return fmt.Errorf("field %q has an invalid date limit", block.Key)
		}
		bounds[i] = at
	}
	if block.MinDate != "" && block.MaxDate != "" && bounds[1].Before(bounds[0]) {
		return fmt.Errorf("field %q has the earliest date after the latest", block.Key)
	}
	return nil
}

func validateDateAnswer(block eventContentModel.Block, value any) error {
	at, ok := dateValue(block, value)
	if !ok {
		return fmt.Errorf("field %q must be a valid date", block.Key)
	}
	if limit, set := ParseDateAnswer(dateMode(block), block.MinDate); block.MinDate != "" && set && at.Before(limit) {
		return fmt.Errorf("field %q is earlier than allowed", block.Key)
	}
	if limit, set := ParseDateAnswer(dateMode(block), block.MaxDate); block.MaxDate != "" && set && at.After(limit) {
		return fmt.Errorf("field %q is later than allowed", block.Key)
	}
	return nil
}

// compareDates applies a condition operator to a date answer.
func compareDates(source eventContentModel.Block, operator string, answer, expected any) bool {
	at, ok := dateValue(source, answer)
	if !ok {
		return false
	}
	want, ok := dateValue(source, expected)
	if !ok {
		return false
	}
	switch operator {
	case "equals":
		return at.Equal(want)
	case "not_equals":
		return !at.Equal(want)
	case OperatorBefore:
		return at.Before(want)
	case OperatorAfter:
		return at.After(want)
	default:
		return false
	}
}
