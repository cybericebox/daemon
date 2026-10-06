// Package platformAnalyticsModel holds the value objects of platform-level
// analytics (docs/EVENT-ANALYTICS.md §8): the report period.
package platformAnalyticsModel

import (
	"strconv"
	"time"
)

const (
	Day = 24 * time.Hour
	// MaxPeriod bounds an explicit period (an "all time" period is clamped to
	// Epoch instead).
	MaxPeriod = 5 * 366 * Day
)

// Epoch is the earliest moment the platform can have data for; "all time"
// starts here. Sections clamp series to their first data point.
var Epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// Period is the half-open report window [From, To), aligned to UTC days.
type Period struct {
	From time.Time
	To   time.Time
	// All is set when the caller asked for no lower bound.
	All bool
}

// NewPeriod resolves the requested window. A missing `to` is now; a missing
// `from` is all time. Bounds are aligned outward to whole UTC days, so the
// window always includes today.
func NewPeriod(from, to *time.Time, now time.Time) (Period, error) {
	end := now
	if to != nil {
		end = *to
	}
	if from != nil && !from.Before(end) {
		return Period{}, ErrPlatformAnalyticsPeriodInvalid.Err()
	}
	p := Period{To: end.UTC().Truncate(Day)}
	if !p.To.Equal(end.UTC()) || p.To.IsZero() {
		p.To = p.To.Add(Day)
	}
	if from == nil {
		p.All = true
		p.From = Epoch
	} else {
		p.From = from.UTC().Truncate(Day)
	}
	if p.From.Before(Epoch) {
		p.From = Epoch
	}
	if !p.From.Before(p.To) || (!p.All && p.To.Sub(p.From) > MaxPeriod) {
		return Period{}, ErrPlatformAnalyticsPeriodInvalid.Err()
	}
	return p, nil
}

// Days is the number of whole days in the window.
func (p Period) Days() int { return int(p.To.Sub(p.From) / Day) }

// Previous is the window of the same length right before this one, for
// trend deltas. There is none for an all-time period.
func (p Period) Previous() (Period, bool) {
	if p.All {
		return Period{}, false
	}
	return Period{From: p.From.Add(-p.To.Sub(p.From)), To: p.From}, true
}

// Key identifies the window in a cache key.
func (p Period) Key() string {
	return strconv.FormatInt(p.From.Unix(), 10) + "-" + strconv.FormatInt(p.To.Unix(), 10)
}
