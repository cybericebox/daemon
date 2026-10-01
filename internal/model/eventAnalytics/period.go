package eventAnalyticsModel

import "time"

// MaxPeriod bounds a report period (a series over it has at most
// MaxPeriod/BucketSize points).
const MaxPeriod = 31 * 24 * time.Hour

// Period is the half-open report window [From, To), aligned to buckets.
type Period struct {
	From time.Time
	To   time.Time
}

// NewPeriod resolves the requested window. A missing bound defaults to the
// event: from its start, to its effective finish (or now while it runs, or
// the start before it). Bounds are aligned outward to whole buckets.
func NewPeriod(from, to *time.Time, start time.Time, finish *time.Time, now time.Time) (Period, error) {
	p := Period{From: start}
	if from != nil {
		p.From = *from
	}
	switch {
	case to != nil:
		p.To = *to
	case finish != nil && finish.Before(now):
		p.To = *finish
	case now.After(start):
		p.To = now
	default:
		p.To = start
	}
	p.From = p.From.Truncate(BucketSize)
	if aligned := p.To.Truncate(BucketSize); !aligned.Equal(p.To) || p.To.Equal(p.From) {
		p.To = aligned.Add(BucketSize)
	}
	if !p.From.Before(p.To) || p.To.Sub(p.From) > MaxPeriod {
		return Period{}, ErrEventAnalyticsPeriodInvalid.Err()
	}
	return p, nil
}
