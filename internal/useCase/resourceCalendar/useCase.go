// Package resourceCalendarUseCase is the resource calendar: it decides how much of the agents' capacity each
// event (and each test laboratory booking) holds over time, places the reservations over the agents by packing,
// lets organizers ask for changes, raises readiness alarms and admits test laboratories. Reservations live only
// here; the agents hold none, only the tenant quota the capacity comes from.
package resourceCalendarUseCase

import (
	"context"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// Amount is the CPU and memory of a reservation or an agent.
type Amount = calModel.Amount

type (
	// Store is the data port of the calendar (satisfied by *resourceCalendarRepo.Repository).
	Store interface {
		Lock(ctx context.Context) error
		CreateReservation(ctx context.Context, v *calModel.Reservation) error
		UpdateReservation(ctx context.Context, v *calModel.Reservation) (bool, error)
		GetReservation(ctx context.Context, id uuid.UUID) (*calModel.Reservation, error)
		GetEventReservation(ctx context.Context, eventID uuid.UUID) (*calModel.Reservation, error)
		ListInWindow(ctx context.Context, w calModel.Window) ([]*calModel.Reservation, error)
		ListEndingAfter(ctx context.Context, after time.Time) ([]*calModel.Reservation, error)
		ListOwnedBookings(ctx context.Context, owner uuid.UUID, after time.Time) ([]*calModel.Reservation, error)
		Labels(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]calModel.Label, error)
		CreateChangeRequest(ctx context.Context, c *calModel.ChangeRequest) error
		GetChangeRequest(ctx context.Context, id uuid.UUID) (*calModel.ChangeRequest, error)
		DecideChangeRequest(ctx context.Context, c *calModel.ChangeRequest) (bool, error)
		ListChangeRequests(ctx context.Context, status *calModel.ChangeStatus, eventID *uuid.UUID) ([]calModel.NamedChangeRequest, error)
		CountPendingChangeRequests(ctx context.Context) (int64, error)
		GetOpenAlarm(ctx context.Context, kind calModel.AlarmKind, reservationID uuid.UUID, agentID *uuid.UUID) (*calModel.Alarm, error)
		CreateAlarm(ctx context.Context, a *calModel.Alarm) error
		UpdateAlarm(ctx context.Context, a *calModel.Alarm) (bool, error)
		GetAlarm(ctx context.Context, id uuid.UUID) (*calModel.Alarm, error)
		ListAlarms(ctx context.Context, onlyOpen bool, limit int32) ([]calModel.NamedAlarm, error)
		ListOpenAlarmsOf(ctx context.Context, reservationID uuid.UUID) ([]*calModel.Alarm, error)
		Settings(ctx context.Context) (calModel.Settings, error)
		SetSettings(ctx context.Context, s calModel.Settings) error
		SaveHold(ctx context.Context, h calModel.TestLabHold) error
		DeleteHold(ctx context.Context, id uuid.UUID) error
		ActiveHolds(ctx context.Context, now time.Time) ([]calModel.TestLabHold, error)
		PurgeHolds(ctx context.Context, now time.Time) (int64, error)
	}

	// Transactor runs fn in one transaction with a Store bound to it; the calendar lock is taken by the caller.
	Transactor interface {
		Do(ctx context.Context, fn func(ctx context.Context, s Store) error) error
	}

	// AgentSource is the agent registry (satisfied by *infrastructureAgentRepo.Repository).
	AgentSource interface {
		ListRecords(ctx context.Context) ([]infraModel.AgentRecord, error)
	}

	// EventSource reads an event (satisfied by *eventRepo.Repository).
	EventSource interface {
		GetByID(ctx context.Context, id uuid.UUID) (eventModel.Event, error)
	}

	// ConfigSource reads the event config for the stand timing (satisfied by *eventConfigRepo.Repository).
	ConfigSource interface {
		Get(ctx context.Context, eventID uuid.UUID) (eventConfigModel.EventConfig, error)
	}

	// Need is what an event's plan asks the agents for: from the resource plan of the event.
	Need struct {
		// Teams is the number of teams reserved for (the maximum, or the teams there are now).
		Teams int
		// PerTeam is the plan of one team: its tasks plus the group's own pods.
		PerTeam Amount
		// LargestDevice is the largest device any task of the event runs.
		LargestDevice Amount
	}

	// Planner computes the Need of an event; it reuses the event's resource plan.
	Planner interface {
		ReservationNeed(ctx context.Context, eventID uuid.UUID) (Need, error)
	}

	// Usage is what the running objects request now, summed per event and per agent.
	Usage struct {
		ByEvent map[uuid.UUID]Amount
		ByAgent map[uuid.UUID]Amount
	}

	// UsageSource reads the requests of the running lab groups.
	UsageSource interface {
		Usage(ctx context.Context, now time.Time) (Usage, error)
	}

	// Notifier tells the people who act on the calendar (the inbox request router).
	Notifier interface {
		ResourceChangeRequested(ctx context.Context, c inboxUseCase.ResourceChange) error
		ResourceChangeDecided(ctx context.Context, c inboxUseCase.ResourceChange, approved bool, by uuid.UUID) error
		ResourceAlarmRaised(ctx context.Context, a inboxUseCase.ResourceAlarm) error
		ResourceAlarmClosed(ctx context.Context, alarmID uuid.UUID, by uuid.UUID) error
	}

	// Config is the calendar's tunables (the environment).
	Config struct {
		// BufferPercent is the default buffer added to an event reservation (15).
		BufferPercent int
		// TailGap is the gap kept after the event end, never shorter (1h).
		TailGap time.Duration
		// LeadMargin is added before the stand deploy lead: capacity must be connected earlier (30m).
		LeadMargin time.Duration
		// SearchHorizon is how far ahead the nearest free window of a test laboratory is looked for (7 days).
		SearchHorizon time.Duration
		// AgentFresh is how recent the capacity of an agent must be for it to count as connected (15m).
		AgentFresh time.Duration
		// TestLabLease is the lease a test laboratory is admitted for when the caller names none (2h).
		TestLabLease time.Duration
	}

	Dependencies struct {
		Store    Store
		Tx       Transactor
		Agents   AgentSource
		Events   EventSource
		Configs  ConfigSource
		Planner  Planner
		Usage    UsageSource
		Notifier Notifier
		Config   Config
		Frame    resourcesModel.Amount
		// Overhead is the group's own pods a test laboratory adds (the VPN and gateway of its group); nil adds none.
		Overhead func() Amount
		Now      func() time.Time
	}

	// ResourceCalendarUseCase is the resource calendar.
	ResourceCalendarUseCase struct {
		store    Store
		tx       Transactor
		agents   AgentSource
		events   EventSource
		configs  ConfigSource
		planner  Planner
		usage    UsageSource
		notifier Notifier
		cfg      Config
		frame    resourcesModel.Amount
		overhead func() Amount
		now      func() time.Time
	}
)

// SetNotifier wires the notifications after the dispatcher exists (the inbox request router).
func (u *ResourceCalendarUseCase) SetNotifier(n Notifier) { u.notifier = n }

// DefaultConfig is the calendar's defaults.
func DefaultConfig() Config {
	return Config{
		BufferPercent: calModel.DefaultBufferPercent, TailGap: calModel.DefaultTailGap, LeadMargin: 30 * time.Minute,
		SearchHorizon: 7 * 24 * time.Hour, AgentFresh: 15 * time.Minute, TestLabLease: 2 * time.Hour,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.BufferPercent <= 0 {
		c.BufferPercent = d.BufferPercent
	}
	if c.TailGap <= 0 {
		c.TailGap = d.TailGap
	}
	if c.LeadMargin < 0 {
		c.LeadMargin = d.LeadMargin
	}
	if c.SearchHorizon <= 0 {
		c.SearchHorizon = d.SearchHorizon
	}
	if c.AgentFresh <= 0 {
		c.AgentFresh = d.AgentFresh
	}
	if c.TestLabLease <= 0 {
		c.TestLabLease = d.TestLabLease
	}
	return c
}

func New(deps Dependencies) *ResourceCalendarUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	frame := deps.Frame
	if frame == (resourcesModel.Amount{}) {
		frame = resourcesModel.DefaultPolicy().Frame
	}
	return &ResourceCalendarUseCase{
		store: deps.Store, tx: deps.Tx, agents: deps.Agents, events: deps.Events, configs: deps.Configs, planner: deps.Planner,
		usage: deps.Usage, notifier: deps.Notifier, cfg: deps.Config.withDefaults(), frame: frame, overhead: deps.Overhead, now: now,
	}
}

// agentState is one agent with what the calendar needs to judge it.
type agentState struct {
	calModel.Agent
	// Connected: its capacity was read recently.
	Connected bool
	// Used: enabled, meets the platform requirements and has a recorded capacity.
	Used bool
	// Why says why an agent is not used: disabled, below_requirements, no_capacity.
	Why string
}

// agentStates reads every agent of the registry; the ones that are used are the enabled ones that meet the
// platform requirements and have a recorded capacity. Per-node room is not reported by the agents yet, so every
// agent counts as one node (see calModel.Agent.Nodes).
func (u *ResourceCalendarUseCase) agentStates(ctx context.Context, now time.Time) ([]agentState, error) {
	records, err := u.agents.ListRecords(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list the agents for the resource calendar").Err()
	}
	out := make([]agentState, 0, len(records))
	for _, r := range records {
		st := agentState{Agent: calModel.Agent{ID: r.ID, Name: r.Name, Priority: r.Priority}}
		switch {
		case !r.Enabled:
			st.Why = "disabled"
		case r.Features != nil && len(r.Features.Limits.UnmetRequirements(u.frame)) > 0:
			st.Why = "below_requirements"
		case r.CapacitySeenAt == nil:
			st.Why = "no_capacity"
		default:
			st.Used = true
			st.Capacity = Amount{CPUMillicores: calModel.Unlimited, MemoryBytes: calModel.Unlimited}
			if r.CapacityCPUMillicores != nil {
				st.Capacity.CPUMillicores = *r.CapacityCPUMillicores
			}
			if r.CapacityMemoryBytes != nil {
				st.Capacity.MemoryBytes = *r.CapacityMemoryBytes
			}
			if r.Features != nil {
				// The reported tenant quota and the recorded capacity both limit the agent: the smaller counts.
				q := r.Features.TenantQuota
				if q.HasCPU {
					st.Capacity.CPUMillicores = min(st.Capacity.CPUMillicores, q.CPUMillicores)
				}
				if q.HasMemory {
					st.Capacity.MemoryBytes = min(st.Capacity.MemoryBytes, q.MemoryBytes)
				}
				st.DeviceMax = Amount{CPUMillicores: r.Features.Limits.DeviceMaxCPUMillicores, MemoryBytes: r.Features.Limits.DeviceMaxMemoryBytes}
			}
			st.Connected = now.Sub(*r.CapacitySeenAt) <= u.cfg.AgentFresh
		}
		out = append(out, st)
	}
	return out, nil
}

// usedAgents are the agents the calendar packs into, in placement order.
func usedAgents(states []agentState) []calModel.Agent {
	var out []calModel.Agent
	for _, s := range states {
		if s.Used {
			out = append(out, s.Agent)
		}
	}
	return calModel.Ordered(out)
}

func connectedAgents(states []agentState) []calModel.Agent {
	var out []calModel.Agent
	for _, s := range states {
		if s.Used && s.Connected {
			out = append(out, s.Agent)
		}
	}
	return calModel.Ordered(out)
}

func platformErr(err error, msg string) error {
	return model.ErrPlatform.WithError(err).WithMessage(msg).Err()
}

func notFound(err error) bool { return repositoryTools.IsObjectNotFoundError(err) }

// inTx runs fn in one transaction holding the calendar lock: no two decisions see the same free room.
func (u *ResourceCalendarUseCase) inTx(ctx context.Context, fn func(ctx context.Context, s Store) error) error {
	return u.tx.Do(ctx, func(ctx context.Context, s Store) error {
		if err := s.Lock(ctx); err != nil {
			return platformErr(err, "Failed to lock the resource calendar")
		}
		return fn(ctx, s)
	})
}

// others filters out the reservation itself.
func others(all []*calModel.Reservation, self uuid.UUID) []*calModel.Reservation {
	out := make([]*calModel.Reservation, 0, len(all))
	for _, r := range all {
		if r.ID != self {
			out = append(out, r)
		}
	}
	return out
}

func sortReservations(rs []*calModel.Reservation) {
	sort.SliceStable(rs, func(i, j int) bool {
		if !rs[i].Window.Start.Equal(rs[j].Window.Start) {
			return rs[i].Window.Start.Before(rs[j].Window.Start)
		}
		return rs[i].ID.String() < rs[j].ID.String()
	})
}
