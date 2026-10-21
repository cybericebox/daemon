package eventConfigModel

import (
	"strings"
	"time"
	"unicode/utf8"

	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

const (
	defaultResultsFreezeMinutes int32 = 60
	maxResultsFreezeMinutes     int32 = 1440
	defaultResultsChartTeams    int32 = 10
	maxResultsChartTeams        int32 = 10
	maxResultsRowsLimit         int32 = 1000
)

type FreezeEndMode string

const (
	FreezeEndAtFinish FreezeEndMode = "at_finish"
	FreezeEndManual   FreezeEndMode = "manual"
)

// OrDefault preserves the automatic finish behavior for legacy zero values.
func (mode FreezeEndMode) OrDefault() FreezeEndMode {
	if mode == "" {
		return FreezeEndAtFinish
	}
	return mode
}

// FreezeBannerLevelOrDefault preserves the warning presentation for legacy settings.
func FreezeBannerLevelOrDefault(level siteBannerModel.Level) siteBannerModel.Level {
	if level == "" {
		return siteBannerModel.LevelWarning
	}
	return level
}

// ResultsSettings is the public results page policy: the scoreboard freeze
// before the finish and how the page presents the table and the chart.
type ResultsSettings struct {
	FreezeEnabled bool
	// FreezeMinutes: the freeze starts this long before the effective finish.
	FreezeMinutes int32
	// FreezeEndMode: automatically at the finish, or only by a moderator's opening.
	FreezeEndMode FreezeEndMode
	// Empty authored text uses the client's localized default message.
	FreezeBannerText  string
	FreezeBannerLevel siteBannerModel.Level
	// OpenedAt: a moderator ended the freeze («Відкрити підсумки»).
	OpenedAt *time.Time
	// LiveFreeze: the live screen honours the freeze too.
	LiveFreeze   bool
	ChartEnabled bool
	ChartTeams   int32
	// RowsLimit: nil shows every ranked row, else the top N.
	RowsLimit *int32
}

func DefaultResultsSettings() ResultsSettings {
	return ResultsSettings{FreezeMinutes: defaultResultsFreezeMinutes, FreezeEndMode: FreezeEndAtFinish, FreezeBannerLevel: siteBannerModel.LevelWarning, LiveFreeze: true, ChartEnabled: true, ChartTeams: defaultResultsChartTeams}
}

func (s ResultsSettings) valid() bool {
	level := FreezeBannerLevelOrDefault(s.FreezeBannerLevel)
	return s.FreezeMinutes >= 1 && s.FreezeMinutes <= maxResultsFreezeMinutes &&
		(s.FreezeEndMode.OrDefault() == FreezeEndAtFinish || s.FreezeEndMode == FreezeEndManual) &&
		utf8.RuneCountInString(strings.TrimSpace(s.FreezeBannerText)) <= siteBannerModel.MaxTextLen &&
		(level == siteBannerModel.LevelInfo || level == siteBannerModel.LevelWarning || level == siteBannerModel.LevelCritical) &&
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

// FreezeActive reports whether the freeze has started and has not been opened.
// Automatic mode ends at the finish; manual mode continues until a moderator opens it.
func (s ResultsSettings) FreezeActive(finish *time.Time, now time.Time) bool {
	frozenAt := s.FrozenAt(finish)
	return frozenAt != nil && s.OpenedAt == nil && !now.Before(*frozenAt) &&
		(s.FreezeEndMode.OrDefault() == FreezeEndManual || now.Before(*finish))
}
