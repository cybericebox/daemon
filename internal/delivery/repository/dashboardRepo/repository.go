// Package dashboardRepo reads the numbers of the platform admin dashboard.
package dashboardRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type Queries interface {
	DashboardFailedStands(
		ctx context.Context,
		arg postgres.DashboardFailedStandsParams,
	) ([]postgres.DashboardFailedStandsRow, error)
	DashboardEventsWithoutReservation(
		ctx context.Context,
		arg postgres.DashboardEventsWithoutReservationParams,
	) ([]postgres.DashboardEventsWithoutReservationRow, error)
	DashboardPendingElevations(ctx context.Context) (postgres.DashboardPendingElevationsRow, error)
	DashboardOpenErrors(
		ctx context.Context,
		rowLimit int32,
	) ([]postgres.DashboardOpenErrorsRow, error)
	DashboardMailErrors(ctx context.Context, since time.Time) (int64, error)
	DashboardCurrentEvents(
		ctx context.Context,
		arg postgres.DashboardCurrentEventsParams,
	) ([]postgres.DashboardCurrentEventsRow, error)
	DashboardTrends(
		ctx context.Context,
		arg postgres.DashboardTrendsParams,
	) ([]postgres.DashboardTrendsRow, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Item is one row of an attention list.
type Item struct {
	ID    uuid.UUID
	Label string
	// Count is how many things stand behind the row (stands of an event, occurrences of an error).
	Count int64
	At    *time.Time
}

// ItemList is the head of a list and the total behind it.
type ItemList struct {
	Total int64
	Items []Item
}

// FailedStands lists the events with failed stands; Total counts the failed stands.
func (r *Repository) FailedStands(
	ctx context.Context,
	now time.Time,
	limit int32,
) (ItemList, error) {
	rows, err := r.q.DashboardFailedStands(
		ctx,
		postgres.DashboardFailedStandsParams{Now: now, RowLimit: limit},
	)
	if err != nil {
		return ItemList{}, err
	}
	list := ItemList{Items: make([]Item, 0, len(rows))}
	for _, row := range rows {
		list.Total = row.TotalStands
		list.Items = append(
			list.Items,
			Item{ID: row.EventID, Label: row.EventName, Count: row.Stands},
		)
	}
	return list, nil
}

// EventsWithoutReservation lists the stand events due before the horizon that have no active reservation.
func (r *Repository) EventsWithoutReservation(
	ctx context.Context,
	now, horizon time.Time,
	limit int32,
) (ItemList, error) {
	rows, err := r.q.DashboardEventsWithoutReservation(
		ctx,
		postgres.DashboardEventsWithoutReservationParams{
			Now:      now,
			Horizon:  horizon,
			RowLimit: limit,
		},
	)
	if err != nil {
		return ItemList{}, err
	}
	list := ItemList{Items: make([]Item, 0, len(rows))}
	for _, row := range rows {
		start := row.StartAt
		list.Total = row.TotalEvents
		list.Items = append(list.Items, Item{ID: row.EventID, Label: row.EventName, At: &start})
	}
	return list, nil
}

// PendingElevations counts the waiting requests and says since when the oldest waits.
func (r *Repository) PendingElevations(ctx context.Context) (int64, *time.Time, error) {
	row, err := r.q.DashboardPendingElevations(ctx)
	if err != nil {
		return 0, nil, err
	}
	if row.Total == 0 {
		return 0, nil, nil
	}
	oldest := row.OldestAt
	return row.Total, &oldest, nil
}

// OpenErrors lists the open error groups, the latest first.
func (r *Repository) OpenErrors(ctx context.Context, limit int32) (ItemList, error) {
	rows, err := r.q.DashboardOpenErrors(ctx, limit)
	if err != nil {
		return ItemList{}, err
	}
	list := ItemList{Items: make([]Item, 0, len(rows))}
	for _, row := range rows {
		seen := row.LastSeenAt
		list.Total = row.TotalGroups
		list.Items = append(
			list.Items,
			Item{ID: row.ID, Label: row.Title, Count: row.Occurrences, At: &seen},
		)
	}
	return list, nil
}

// MailErrors counts the deliveries that failed since the moment.
func (r *Repository) MailErrors(ctx context.Context, since time.Time) (int64, error) {
	return r.q.DashboardMailErrors(ctx, since)
}

// CurrentEvent is an event that runs now or starts soon.
type CurrentEvent struct {
	ID                                            uuid.UUID
	Name, Tag                                     string
	StartAt                                       time.Time
	FinishAt                                      *time.Time
	Running                                       bool
	Participants, Teams, StandsReady, StandsTotal int64
}

// CurrentEvents lists the events that run now or start before the horizon.
func (r *Repository) CurrentEvents(
	ctx context.Context,
	now, horizon time.Time,
	limit int32,
) ([]CurrentEvent, error) {
	rows, err := r.q.DashboardCurrentEvents(
		ctx,
		postgres.DashboardCurrentEventsParams{Now: now, Horizon: horizon, RowLimit: limit},
	)
	if err != nil {
		return nil, err
	}
	out := make([]CurrentEvent, 0, len(rows))
	for _, row := range rows {
		item := CurrentEvent{
			ID:           row.ID,
			Name:         row.Name,
			Tag:          row.Tag,
			StartAt:      row.StartAt,
			Running:      row.Running,
			Participants: row.Participants,
			Teams:        row.Teams,
			StandsReady:  row.StandsReady,
			StandsTotal:  row.StandsTotal,
		}
		if row.FinishAt.Valid {
			finish := row.FinishAt.Time
			item.FinishAt = &finish
		}
		out = append(out, item)
	}
	return out, nil
}

// TrendDay is one day of the trends.
type TrendDay struct {
	Day                                time.Time
	Registrations, ActiveUsers, Solves int64
}

// Trends reads days daily points from the day on.
func (r *Repository) Trends(ctx context.Context, from time.Time, days int32) ([]TrendDay, error) {
	rows, err := r.q.DashboardTrends(ctx, postgres.DashboardTrendsParams{FromDay: from, Days: days})
	if err != nil {
		return nil, err
	}
	out := make([]TrendDay, 0, len(rows))
	for _, row := range rows {
		out = append(
			out,
			TrendDay{
				Day:           row.Day,
				Registrations: row.Registrations,
				ActiveUsers:   row.ActiveUsers,
				Solves:        row.Solves,
			},
		)
	}
	return out, nil
}
