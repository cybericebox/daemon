package platformAnalytics

import (
	"time"

	"github.com/gofrs/uuid"

	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

type (
	// EventsView is the «Заходи» report.
	EventsView struct {
		Period platformAnalyticsModel.Period
		Totals EventsTotals
		// Series has one point per UTC day of the period.
		Series []EventDayView
		// Statuses lists every lifecycle status, including empty ones.
		Statuses []EventStatusView
		// Events are the newest events of the period (at most EventsLimit);
		// EventsTotal is how many there are in all.
		Events      []EventRowView
		EventsTotal int64
		EventsLimit int
		Upcoming    []UpcomingEventView
	}

	// EventsTotals: Events is the number of events of the period (their
	// schedule overlaps it); the rest are sums over the series.
	EventsTotals struct {
		Events, Created, Started, Registrations int64
	}

	EventDayView struct {
		Day                                         time.Time
		EventsCreated, EventsStarted, Registrations int64
	}

	EventStatusView struct {
		// Status: not_published, published, started, finished or withdrawn.
		Status string
		Events int64
	}

	// EventRowView: participants are registrations without pending
	// invitations; CompletionRate is the share of teams with at least one
	// solve (0..1, 0 without teams); DurationSeconds is set once the event
	// has a finish. StartAt is the zero time for an event never scheduled.
	EventRowView struct {
		ID                          uuid.UUID
		Tag, Name, Status           string
		StartAt                     time.Time
		FinishAt                    *time.Time
		DurationSeconds             *int64
		Participants, Teams, Solves int64
		TeamsSolved                 int64
		CompletionRate              float64
	}

	UpcomingEventView struct {
		ID            uuid.UUID
		Tag, Name     string
		StartAt       time.Time
		Published     bool
		Registrations int64
	}
)
