package eventConfigModel

import "time"

const (
	defaultResultsFreezeMinutes int32 = 60
	maxResultsFreezeMinutes     int32 = 1440
	defaultResultsChartTeams    int32 = 10
	maxResultsChartTeams        int32 = 10
	maxResultsRowsLimit         int32 = 1000
)

// ResultsSettings is the public results page policy: the scoreboard freeze
// before the finish and how the page presents the table and the chart.
type ResultsSettings struct {
	FreezeEnabled bool
	// FreezeMinutes: the freeze starts this long before the effective finish.
	FreezeMinutes int32
	// OpenedAt: a moderator ended the freeze early («Відкрити підсумки»).
	OpenedAt *time.Time
	// LiveFreeze: the live screen honours the freeze too.
	LiveFreeze   bool
	ChartEnabled bool
	ChartTeams   int32
	// RowsLimit: nil shows every ranked row, else the top N.
	RowsLimit *int32
}

func DefaultResultsSettings() ResultsSettings {
	return ResultsSettings{FreezeMinutes: defaultResultsFreezeMinutes, LiveFreeze: true, ChartEnabled: true, ChartTeams: defaultResultsChartTeams}
}

func (s ResultsSettings) valid() bool {
	return s.FreezeMinutes >= 1 && s.FreezeMinutes <= maxResultsFreezeMinutes &&
		s.ChartTeams >= 1 && s.ChartTeams <= maxResultsChartTeams &&
		(s.RowsLimit == nil || (*s.RowsLimit >= 1 && *s.RowsLimit <= maxResultsRowsLimit))
}

// FrozenAt is the freeze moment for an event finishing at finish: nil when
// the freeze is off or the event has no finish.
func (s ResultsSettings) FrozenAt(finish *time.Time) *time.Time {
	if !s.FreezeEnabled || finish == nil {
		return nil
	}
	at := finish.Add(-time.Duration(s.FreezeMinutes) * time.Minute)
	return &at
}

// FreezeActive reports whether the table is frozen now: inside the window
// before the finish and not opened early by a moderator. It ends by itself at
// the finish.
func (s ResultsSettings) FreezeActive(finish *time.Time, now time.Time) bool {
	frozenAt := s.FrozenAt(finish)
	return frozenAt != nil && s.OpenedAt == nil && !now.Before(*frozenAt) && now.Before(*finish)
}
