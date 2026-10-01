package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// EventsQueries are the sqlc statements of the events section.
type EventsQueries interface {
	ListPlatformAnalyticsEventSeries(context.Context, postgres.ListPlatformAnalyticsEventSeriesParams) ([]postgres.ListPlatformAnalyticsEventSeriesRow, error)
	ListPlatformAnalyticsEventStatuses(context.Context, postgres.ListPlatformAnalyticsEventStatusesParams) ([]postgres.ListPlatformAnalyticsEventStatusesRow, error)
	ListPlatformAnalyticsEvents(context.Context, postgres.ListPlatformAnalyticsEventsParams) ([]postgres.ListPlatformAnalyticsEventsRow, error)
	ListPlatformAnalyticsUpcomingEvents(context.Context, postgres.ListPlatformAnalyticsUpcomingEventsParams) ([]postgres.ListPlatformAnalyticsUpcomingEventsRow, error)
}

type (
	// EventDay is one UTC day of the events series.
	EventDay struct {
		Day                                         time.Time
		EventsCreated, EventsStarted, Registrations int64
	}

	// EventStatusCount is the number of events in a lifecycle status
	// (eventModel.LifecycleStatus code).
	EventStatusCount struct {
		Status int16
		Events int64
	}

	// EventRow is one event of the period with its aggregates. FinishAt is
	// nil while no finish is set.
	EventRow struct {
		ID                                       uuid.UUID
		Tag, Name, InternalName                  string
		Status                                   int16
		Configured                               bool
		StartAt                                  time.Time
		FinishAt                                 *time.Time
		Participants, Teams, Solves, TeamsSolved int64
	}

	// UpcomingEvent is a scheduled event that has not started.
	UpcomingEvent struct {
		ID            uuid.UUID
		Tag, Name     string
		StartAt       time.Time
		Published     bool
		Registrations int64
	}
)

// EventSeries reads the per-day events series of [from, to).
func (r *Repository) EventSeries(ctx context.Context, from, to time.Time) ([]EventDay, error) {
	rows, err := r.q.ListPlatformAnalyticsEventSeries(ctx, postgres.ListPlatformAnalyticsEventSeriesParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]EventDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, EventDay{Day: row.Day.Time, EventsCreated: row.EventsCreated, EventsStarted: row.EventsStarted, Registrations: row.Registrations})
	}
	return out, nil
}

// EventStatuses counts the events of the period per lifecycle status at asOf.
func (r *Repository) EventStatuses(ctx context.Context, from, to, asOf time.Time) ([]EventStatusCount, error) {
	rows, err := r.q.ListPlatformAnalyticsEventStatuses(ctx, postgres.ListPlatformAnalyticsEventStatusesParams{FromAt: from, ToAt: to, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	out := make([]EventStatusCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, EventStatusCount{Status: row.Status, Events: row.Events})
	}
	return out, nil
}

// Events reads the newest events of the period (at most limit) and the size
// of the whole scope.
func (r *Repository) Events(ctx context.Context, from, to, asOf time.Time, limit int32) (rows []EventRow, total int64, err error) {
	list, err := r.q.ListPlatformAnalyticsEvents(ctx, postgres.ListPlatformAnalyticsEventsParams{FromAt: from, ToAt: to, AsOf: asOf, RowLimit: limit})
	if err != nil {
		return nil, 0, err
	}
	rows = make([]EventRow, 0, len(list))
	for _, row := range list {
		total = row.Total
		rows = append(rows, EventRow{
			ID: row.EventID, Tag: row.Tag, Name: row.Name,
			Status: row.Status, Configured: row.Configured, StartAt: row.StartAt, FinishAt: eventFinishOrNil(row.FinishAt),
			Participants: row.Participants, Teams: row.Teams, Solves: row.Solves, TeamsSolved: row.TeamsSolved,
		})
	}
	return rows, total, nil
}

// UpcomingEvents reads the next scheduled events by start date.
func (r *Repository) UpcomingEvents(ctx context.Context, asOf time.Time, limit int32) ([]UpcomingEvent, error) {
	rows, err := r.q.ListPlatformAnalyticsUpcomingEvents(ctx, postgres.ListPlatformAnalyticsUpcomingEventsParams{AsOf: asOf, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]UpcomingEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, UpcomingEvent{
			ID: row.EventID, Tag: row.Tag, Name: row.Name,
			StartAt: row.StartAt, Published: row.Published, Registrations: row.Registrations,
		})
	}
	return out, nil
}

// eventFinishOrNil maps the SQL "none" sentinel (the epoch) to nil.
func eventFinishOrNil(value time.Time) *time.Time {
	if value.Unix() <= 0 {
		return nil
	}
	return &value
}
