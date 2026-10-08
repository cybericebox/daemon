package useCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

// calendarTx runs a calendar decision in one unit of work.
type calendarTx struct {
	uow postgres.IUnitOfWorker[resourceCalendarRepo.Queries]
}

func (t calendarTx) Do(ctx context.Context, fn func(context.Context, calendarUseCase.Store) error) error {
	txCtx, queries, unit, err := t.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = unit.Restore() }()
	if err = fn(txCtx, resourceCalendarRepo.New(queries)); err != nil {
		return err
	}
	return unit.Save()
}

// calendarNeeds gives the calendar the event's resource plan (the plan per team, the teams, the largest device).
type calendarNeeds struct{ events *eventUseCase.EventUseCase }

func (c calendarNeeds) ReservationNeed(ctx context.Context, eventID uuid.UUID) (calendarUseCase.Need, error) {
	need, err := c.events.ReservationNeed(ctx, eventID)
	if err != nil {
		return calendarUseCase.Need{}, err
	}
	return calendarUseCase.Need{Teams: need.Teams, PerTeam: need.PerTeam, LargestDevice: need.LargestDevice}, nil
}

// calendarUsage sums what the running lab groups request, per event and per agent, from the monitoring the
// agents report.
type calendarUsage struct {
	observations *eventLabObservationRepo.Repository
	allocations  *eventLabAllocationRepo.Repository
	events       *eventUseCase.EventUseCase
}

func (c calendarUsage) Usage(ctx context.Context, now time.Time) (calendarUseCase.Usage, error) {

	if c.allocations != nil && c.events != nil {
		return c.heldUsage(ctx, now)
	}
	current, err := c.observations.CurrentPlatform(ctx, false, now.Add(-labMonitoringModel.RecentWindow), now)
	if err != nil {
		return calendarUseCase.Usage{}, err
	}
	out := calendarUseCase.Usage{ByEvent: map[uuid.UUID]calendarUseCase.Amount{}, ByAgent: map[uuid.UUID]calendarUseCase.Amount{}}
	for _, item := range current {
		res := labMonitoringModel.PayloadResources(item.Payload)
		if !res.Known {
			continue
		}
		requested := calendarUseCase.Amount{CPUMillicores: res.RequestedCPU, MemoryBytes: res.RequestedMemory}
		out.ByEvent[item.EventID] = out.ByEvent[item.EventID].Add(requested)
		if agent, parseErr := uuid.FromString(item.AgentID); parseErr == nil {
			out.ByAgent[agent] = out.ByAgent[agent].Add(requested)
		}
	}
	return out, nil
}

// groupOverhead is the pods a test laboratory's group brings (the VPN of one user and the gateway of one
// internet lab), by the formula of the agents; nil when no agent reported its sizing.
func groupOverhead(agent any) func() calendarUseCase.Amount {
	sizer, ok := agent.(interface {
		GroupSizes(infraModel.GroupPlan) (infraModel.GroupSizes, bool)
	})
	if !ok {
		return nil
	}
	return func() calendarUseCase.Amount {
		sizes, known := sizer.GroupSizes(infraModel.GroupPlan{MaxUsers: 1, InternetLabs: 1})
		if !known {
			return calendarUseCase.Amount{}
		}
		return sizes.Total()
	}
}

func (c calendarUsage) heldUsage(ctx context.Context, now time.Time) (calendarUseCase.Usage, error) {
	labs, err := c.allocations.PlatformLabs(ctx)
	if err != nil {
		return calendarUseCase.Usage{}, err
	}
	groups, err := c.allocations.PlatformGroups(ctx)
	if err != nil {
		return calendarUseCase.Usage{}, err
	}
	unknown, e := c.allocations.UnaccountedStarts(ctx)
	if e != nil {
		return calendarUseCase.Usage{}, e
	}
	out := calendarUseCase.Usage{UnaccountedByEvent: unknown, ByEvent: map[uuid.UUID]calendarUseCase.Amount{}, ByAgent: map[uuid.UUID]calendarUseCase.Amount{}, StorageByEvent: map[uuid.UUID]eventLabModel.StorageBudget{}, ByEventObservation: map[uuid.UUID]eventLabModel.ResourceTotals{}, Observation: eventLabModel.ResourceTotals{Complete: true, Storage: eventLabModel.StorageBudget{PhysicalKnown: true}}}
	ids := map[uuid.UUID]bool{}
	for id := range unknown {
		ids[id] = true
	}
	byTeam := map[uuid.UUID]calendarUseCase.Amount{}
	for _, l := range labs {
		ids[l.EventID] = true
		h := l.HeldCompute()
		byTeam[l.TeamID] = byTeam[l.TeamID].Add(calendarUseCase.Amount{CPUMillicores: h.CPUMillicores, MemoryBytes: h.MemoryBytes})
	}
	for _, g := range groups {
		ids[g.EventID] = true
		h := g.Lifecycle.HeldCompute()
		byTeam[g.TeamID] = byTeam[g.TeamID].Add(calendarUseCase.Amount{CPUMillicores: h.CPUMillicores, MemoryBytes: h.MemoryBytes})
	}
	for id := range ids {
		t, e := c.events.ResourceTotals(ctx, id, now)
		if e != nil {
			return calendarUseCase.Usage{}, e
		}
		out.ByEvent[id] = calendarUseCase.Amount{CPUMillicores: t.Held.CPUMillicores, MemoryBytes: t.Held.MemoryBytes}
		out.StorageByEvent[id] = t.Storage
		out.ByEventObservation[id] = t
		total := &out.Observation
		total.Held.CPUMillicores += t.Held.CPUMillicores
		total.Held.MemoryBytes += t.Held.MemoryBytes
		total.PendingStarts.CPUMillicores += t.PendingStarts.CPUMillicores
		total.PendingStarts.MemoryBytes += t.PendingStarts.MemoryBytes
		total.GroupServices.CPUMillicores += t.GroupServices.CPUMillicores
		total.GroupServices.MemoryBytes += t.GroupServices.MemoryBytes
		total.Storage.SnapshotQuotaBytes += t.Storage.SnapshotQuotaBytes
		total.Storage.PhysicalStorageBytes += t.Storage.PhysicalStorageBytes
		total.Storage.PhysicalKnown = total.Storage.PhysicalKnown && t.Storage.PhysicalKnown
		total.Complete = total.Complete && t.Complete
		if t.ObservedAt != nil && (total.ObservedAt == nil || t.ObservedAt.Before(*total.ObservedAt)) {
			at := *t.ObservedAt
			total.ObservedAt = &at
		}
	}
	if len(ids) == 0 {
		out.Observation.Complete = false
		out.Observation.Storage.PhysicalKnown = false
	}
	// Mapping a held ledger to its last known owner is separate from telemetry
	// freshness. Stale/unknown measurements never subtract held allocations.
	current, e := c.observations.CurrentPlatform(ctx, true, time.Time{}, now)
	if e != nil {
		return calendarUseCase.Usage{}, e
	}
	for _, item := range current {
		if agent, e := uuid.FromString(item.AgentID); e == nil {
			out.ByAgent[agent] = out.ByAgent[agent].Add(byTeam[item.EventTeamID])
		}
	}
	return out, nil
}
