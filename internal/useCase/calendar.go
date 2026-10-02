package useCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
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
}

func (c calendarUsage) Usage(ctx context.Context, now time.Time) (calendarUseCase.Usage, error) {
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
