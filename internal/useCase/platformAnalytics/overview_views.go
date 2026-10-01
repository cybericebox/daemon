package platformAnalytics

import (
	"time"

	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

type (
	// OverviewPeriodView is the resolved window of a report.
	OverviewPeriodView struct {
		From, To time.Time
		// All: no lower bound was asked for.
		All bool
		// Previous is the window of the same length before this one; nil for all time.
		Previous *OverviewPeriodBoundsView
	}

	OverviewPeriodBoundsView struct{ From, To time.Time }

	// OverviewMetricView is a value of the period and the previous period's one (nil
	// for all time, when there is no previous period).
	OverviewMetricView struct {
		Value    int64
		Previous *int64
	}

	OverviewView struct {
		Period       OverviewPeriodView
		Users        OverviewUsersView
		Events       OverviewEventsView
		Participants OverviewParticipantsView
		Activity     OverviewActivityView
		Mail         OverviewMailView
		Stands       OverviewStandsView
		Series       OverviewSeriesView
	}

	// OverviewUsersView: Total is now; New are registered in the period;
	// Active had recorded activity in the period.
	OverviewUsersView struct {
		Total  int64
		New    OverviewMetricView
		Active OverviewMetricView
	}

	// OverviewEventsView counts events by lifecycle status now (draft =
	// not available or not published, running = started, finished also holds
	// withdrawn); New were created in the period.
	OverviewEventsView struct {
		Draft, Published, Running, Finished, Archived, Total int64
		New                                                  OverviewMetricView
	}

	OverviewParticipantsView struct{ Registered, Approved OverviewMetricView }

	OverviewActivityView struct{ Attempts, Solves OverviewMetricView }

	OverviewMailView struct{ Sent, Failed OverviewMetricView }

	// OverviewStandsView: Ready, Creating, Failed are the stands right now;
	// Failures counts the failures logged in the period.
	OverviewStandsView struct {
		Ready, Creating, Failed int64
		Failures                OverviewMetricView
	}

	OverviewSeriesView struct {
		NewUsers []OverviewDayNewView
		Activity []OverviewDayActivityView
		Mail     []OverviewDayMailView
	}

	OverviewDayNewView struct {
		Day time.Time
		New int64
	}
	OverviewDayActivityView struct {
		Day              time.Time
		Attempts, Solves int64
	}
	OverviewDayMailView struct {
		Day          time.Time
		Sent, Failed int64
	}
)

func overviewPeriodViewOf(p platformAnalyticsModel.Period) OverviewPeriodView {
	v := OverviewPeriodView{From: p.From, To: p.To, All: p.All}
	if prev, ok := p.Previous(); ok {
		v.Previous = &OverviewPeriodBoundsView{From: prev.From, To: prev.To}
	}
	return v
}
