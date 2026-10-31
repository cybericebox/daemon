// Package adminDashboard builds the platform admin dashboard (docs/specs/ADMIN.md §3) in one answer: only the blocks
// the caller has the permission for are present.
package adminDashboard

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dashboardRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Attention kinds: what needs the admin and where to fix it (Target is a place of the admin panel: stands,
// reservations, elevations, errors, mail; the items point at events or error groups).
const (
	KindFailedStands        = "failed_stands"
	KindEventsNoReservation = "events_without_reservation"
	KindPendingElevations   = "pending_elevations"
	KindOpenErrors          = "open_errors"
	KindMailErrors          = "mail_errors"
	TargetStands            = "stands"
	TargetReservations      = "reservations"
	TargetElevations        = "elevations"
	TargetErrors            = "errors"
	TargetMail              = "mail"
	attentionItems          = 5
	currentEventsLimit      = 20
	upcomingDays            = 7
	reservationHorizonDays  = 14
	trendDays               = 7
	mailErrorsDays          = 7
	capacityWindow          = 15 * time.Minute
	capacityStepSeconds     = 900.0
	hoursInDay              = 24 * time.Hour
)

type (
	Store interface {
		FailedStands(ctx context.Context, now time.Time, limit int32) (dashboardRepo.ItemList, error)
		EventsWithoutReservation(ctx context.Context, now, horizon time.Time, limit int32) (dashboardRepo.ItemList, error)
		PendingElevations(ctx context.Context) (int64, *time.Time, error)
		OpenErrors(ctx context.Context, limit int32) (dashboardRepo.ItemList, error)
		MailErrors(ctx context.Context, since time.Time) (int64, error)
		CurrentEvents(ctx context.Context, now, horizon time.Time, limit int32) ([]dashboardRepo.CurrentEvent, error)
		Trends(ctx context.Context, from time.Time, days int32) ([]dashboardRepo.TrendDay, error)
	}

	// Infra is the cluster picture (satisfied by *platformAnalyticsRepo.Repository).
	Infra interface {
		InfraStandCounts(ctx context.Context) (platformAnalyticsRepo.InfraStandCounts, error)
		InfraCapacity(ctx context.Context, from, to time.Time, stepSeconds float64) ([]platformAnalyticsRepo.InfraCapacity, error)
	}

	Dashboard struct {
		store Store
		infra Infra
		now   func() time.Time
	}

	AttentionItem struct {
		ID    uuid.UUID
		Label string
		Count int64
		At    *time.Time
	}

	// Attention is one thing that needs the admin: Count of them, Target where to go, Items the first few.
	Attention struct {
		Kind   string
		Count  int64
		Target string
		Items  []AttentionItem
	}

	CurrentEvent = dashboardRepo.CurrentEvent

	Trends struct {
		Days []time.Time
		// Registrations needs users.read, ActiveUsers users.read, Solves analytics.read: a series the caller may
		// not see is nil.
		Registrations, ActiveUsers, Solves []int64
	}

	Infrastructure struct {
		CPUAllocatableMillicores, CPUReservedMillicores int64
		MemoryAllocatableBytes, MemoryReservedBytes     int64
		Agents                                          int64
		ActiveStands, FailedStands                      int64
	}

	// View is the dashboard; a block the caller has no permission for is nil.
	View struct {
		Attention          []Attention
		CurrentAndUpcoming []CurrentEvent
		Trends7d           *Trends
		Infrastructure     *Infrastructure
	}
)

func New(store Store, infra Infra) *Dashboard {
	return &Dashboard{store: store, infra: infra, now: time.Now}
}

// SetClock replaces the clock (tests).
func (d *Dashboard) SetClock(now func() time.Time) { d.now = now }

func allowed(ctx context.Context, permission rbac.Permission) bool {
	return rbac.HasPermissionInContext(ctx, permission)
}

// GetAdminDashboard builds the blocks the caller may see.
func (d *Dashboard) GetAdminDashboard(ctx context.Context) (View, error) {
	now := d.now().UTC()
	var view View
	var err error
	if view.Attention, err = d.attention(ctx, now); err != nil {
		return View{}, err
	}
	if allowed(ctx, rbac.PermEventsRead) {
		if view.CurrentAndUpcoming, err = d.store.CurrentEvents(ctx, now, now.Add(upcomingDays*hoursInDay), currentEventsLimit); err != nil {
			return View{}, fail(err, "Failed to read the current events")
		}
	}
	if view.Trends7d, err = d.trends(ctx, now); err != nil {
		return View{}, err
	}
	if allowed(ctx, rbac.PermInfrastructureRead) {
		if view.Infrastructure, err = d.infrastructure(ctx, now); err != nil {
			return View{}, err
		}
	}
	return view, nil
}

func (d *Dashboard) attention(ctx context.Context, now time.Time) ([]Attention, error) {
	out := []Attention{}
	add := func(kind, target string, list dashboardRepo.ItemList) {
		if list.Total == 0 {
			return
		}
		item := Attention{Kind: kind, Count: list.Total, Target: target, Items: make([]AttentionItem, 0, len(list.Items))}
		for _, i := range list.Items {
			item.Items = append(item.Items, AttentionItem{ID: i.ID, Label: i.Label, Count: i.Count, At: i.At})
		}
		out = append(out, item)
	}
	if allowed(ctx, rbac.PermInfrastructureRead) {
		failed, err := d.store.FailedStands(ctx, now, attentionItems)
		if err != nil {
			return nil, fail(err, "Failed to read the failed stands")
		}
		add(KindFailedStands, TargetStands, failed)
		missing, err := d.store.EventsWithoutReservation(ctx, now, now.Add(reservationHorizonDays*hoursInDay), attentionItems)
		if err != nil {
			return nil, fail(err, "Failed to read the events without a reservation")
		}
		add(KindEventsNoReservation, TargetReservations, missing)
	}
	if allowed(ctx, rbac.PermExercisesElevationsRead) {
		total, oldest, err := d.store.PendingElevations(ctx)
		if err != nil {
			return nil, fail(err, "Failed to read the pending elevations")
		}
		if total > 0 {
			out = append(out, Attention{Kind: KindPendingElevations, Count: total, Target: TargetElevations, Items: []AttentionItem{{Count: total, At: oldest}}})
		}
	}
	if allowed(ctx, rbac.PermPlatformErrorsRead) {
		open, err := d.store.OpenErrors(ctx, attentionItems)
		if err != nil {
			return nil, fail(err, "Failed to read the open errors")
		}
		add(KindOpenErrors, TargetErrors, open)
	}
	if allowed(ctx, rbac.PermAnalyticsRead) {
		failed, err := d.store.MailErrors(ctx, now.Add(-mailErrorsDays*hoursInDay))
		if err != nil {
			return nil, fail(err, "Failed to read the mail errors")
		}
		if failed > 0 {
			out = append(out, Attention{Kind: KindMailErrors, Count: failed, Target: TargetMail})
		}
	}
	return out, nil
}

func (d *Dashboard) trends(ctx context.Context, now time.Time) (*Trends, error) {
	users, solves := allowed(ctx, rbac.PermUsersRead), allowed(ctx, rbac.PermAnalyticsRead)
	if !users && !solves {
		return nil, nil
	}
	first := now.Truncate(hoursInDay).Add(-(trendDays - 1) * hoursInDay)
	days, err := d.store.Trends(ctx, first, trendDays)
	if err != nil {
		return nil, fail(err, "Failed to read the trends")
	}
	out := &Trends{Days: make([]time.Time, 0, len(days))}
	if users {
		out.Registrations, out.ActiveUsers = []int64{}, []int64{}
	}
	if solves {
		out.Solves = []int64{}
	}
	for _, day := range days {
		out.Days = append(out.Days, day.Day)
		if users {
			out.Registrations, out.ActiveUsers = append(out.Registrations, day.Registrations), append(out.ActiveUsers, day.ActiveUsers)
		}
		if solves {
			out.Solves = append(out.Solves, day.Solves)
		}
	}
	return out, nil
}

func (d *Dashboard) infrastructure(ctx context.Context, now time.Time) (*Infrastructure, error) {
	counts, err := d.infra.InfraStandCounts(ctx)
	if err != nil {
		return nil, fail(err, "Failed to count the stands")
	}
	out := &Infrastructure{ActiveStands: counts.Creating + counts.Ready, FailedStands: counts.Failed}
	capacity, err := d.infra.InfraCapacity(ctx, now.Add(-capacityWindow), now.Add(time.Second), capacityStepSeconds)
	if err != nil {
		return nil, fail(err, "Failed to read the cluster capacity")
	}
	if len(capacity) > 0 {
		last := capacity[len(capacity)-1]
		out.CPUAllocatableMillicores, out.CPUReservedMillicores = last.AllocatableCPUMillis, last.RequestedCPUMillis
		out.MemoryAllocatableBytes, out.MemoryReservedBytes, out.Agents = last.AllocatableMemory, last.RequestedMemory, last.Agents
	}
	return out, nil
}

func fail(err error, message string) error {
	return model.ErrPlatform.WithError(err).WithMessage(message).Err()
}
