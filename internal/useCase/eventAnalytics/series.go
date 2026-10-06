package eventAnalytics

import (
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// denseSeries lays the stored (non-empty) buckets over every bucket of the
// period, so charts get a point per 5 minutes.
func denseSeries(points []eventAnalyticsRepo.SeriesPoint, period eventAnalyticsModel.Period) []SeriesPointView {
	byAt := make(map[int64]eventAnalyticsRepo.SeriesPoint, len(points))
	for _, p := range points {
		byAt[p.At.Unix()] = p
	}
	out := make([]SeriesPointView, 0, int(period.To.Sub(period.From)/eventAnalyticsModel.BucketSize))
	for at := period.From; at.Before(period.To); at = at.Add(eventAnalyticsModel.BucketSize) {
		p := byAt[at.Unix()]
		out = append(out, SeriesPointView{At: at, Attempts: p.Attempts, Correct: p.Correct, Solves: p.Solves, Opens: p.Opens})
	}
	return out
}
