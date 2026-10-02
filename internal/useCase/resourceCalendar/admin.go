package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	infraUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
)

// MaxTimelineRange is the longest range one timeline request covers.
const MaxTimelineRange = 31 * 24 * time.Hour

func capacityView(states []agentState, pool Amount) CapacityView {
	view := CapacityView{TestPool: pool, Agents: make([]AgentCapacityView, 0, len(states))}
	reported, used := 0, 0
	for _, st := range states {
		a := AgentCapacityView{
			ID: st.ID, Name: st.Name, Priority: st.Priority, Used: st.Used, Why: st.Why, Connected: st.Connected, DeviceMax: st.DeviceMax,
		}
		if st.Used {
			used++
			if st.NodesReported {
				reported++
				a.Nodes = append([]Amount(nil), st.Nodes...)
			}
			a.CPUUnlimited, a.MemoryUnlimited = st.Capacity.CPUMillicores >= calModel.Unlimited, st.Capacity.MemoryBytes >= calModel.Unlimited
			if !a.CPUUnlimited {
				a.Capacity.CPUMillicores = st.Capacity.CPUMillicores
				view.Total.CPUMillicores += st.Capacity.CPUMillicores
			}
			if !a.MemoryUnlimited {
				a.Capacity.MemoryBytes = st.Capacity.MemoryBytes
				view.Total.MemoryBytes += st.Capacity.MemoryBytes
			}
			view.CPUUnlimited = view.CPUUnlimited || a.CPUUnlimited
			view.MemoryUnlimited = view.MemoryUnlimited || a.MemoryUnlimited
		}
		view.Agents = append(view.Agents, a)
	}
	view.PerNodeRoomReported = used > 0 && reported == used
	return view
}

// GetCapacity is the capacity of the agents as the calendar uses it (admin).
func (u *ResourceCalendarUseCase) GetResourceCalendarCapacity(ctx context.Context) (CapacityView, error) {
	states, err := u.agentStates(ctx, u.now().UTC())
	if err != nil {
		return CapacityView{}, err
	}
	settings, err := u.store.Settings(ctx)
	if err != nil {
		return CapacityView{}, platformErr(err, "Failed to read the calendar settings")
	}
	return capacityView(states, settings.TestPool), nil
}

// GetTimeline is the calendar over a range of slots: the reservations, the total reserved, the capacity and the
// conflicts (admin). The range is at most 31 days.
func (u *ResourceCalendarUseCase) GetResourceCalendarTimeline(ctx context.Context, from, to time.Time) (TimelineView, error) {
	w, err := calModel.NewWindow(from, to)
	if err != nil || w.Duration() > MaxTimelineRange {
		return TimelineView{}, calModel.ErrWindowInvalid.Err()
	}
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return TimelineView{}, err
	}
	settings, err := u.store.Settings(ctx)
	if err != nil {
		return TimelineView{}, platformErr(err, "Failed to read the calendar settings")
	}
	rs, err := u.store.ListInWindow(ctx, w)
	if err != nil {
		return TimelineView{}, platformErr(err, "Failed to read the reservations")
	}
	sortReservations(rs)
	ids := make([]uuid.UUID, 0, len(rs))
	for _, r := range rs {
		if r.EventID != nil {
			ids = append(ids, r.ID)
		}
	}
	labels, err := u.store.Labels(ctx, ids)
	if err != nil {
		return TimelineView{}, platformErr(err, "Failed to read the event names")
	}
	alarms, err := u.openAlarmsByReservation(ctx, states)
	if err != nil {
		return TimelineView{}, err
	}
	conflicts := calModel.FindConflicts(w, usedAgents(states), rs, settings.TestPool)
	var usage Usage
	if u.usage != nil {
		if got, uErr := u.usage.Usage(ctx, now); uErr == nil {
			usage = got
		} else {
			log.Warn().Err(uErr).Msg("Resource usage unavailable for the timeline")
		}
	}
	view := TimelineView{
		From: w.Start, To: w.End, SlotMinutes: int(calModel.SlotDuration / time.Minute), Capacity: capacityView(states, settings.TestPool),
		Reservations: make([]ReservationView, 0, len(rs)), Reserved: []SegmentView{}, Conflicts: conflictViews(conflicts),
	}
	for _, seg := range calModel.ReservedSegments(w, rs) {
		view.Reserved = append(view.Reserved, SegmentView{From: seg.Window.Start, To: seg.Window.End, Reserved: seg.Reserved})
	}
	inConflict := map[uuid.UUID]bool{}
	for _, c := range conflicts {
		for _, id := range c.Reservations {
			inConflict[id] = true
		}
	}
	for _, r := range rs {
		v := u.reservationView(r, states, labels, alarms[r.ID])
		v.Covered = r.Unplaced == 0 && !inConflict[r.ID]
		if r.EventID != nil {
			v.Used = usage.ByEvent[*r.EventID]
		}
		view.Reservations = append(view.Reservations, v)
	}
	return view, nil
}

func (u *ResourceCalendarUseCase) openAlarmsByReservation(ctx context.Context, states []agentState) (map[uuid.UUID][]AlarmView, error) {
	rows, err := u.store.ListAlarms(ctx, true, alarmListLimit)
	if err != nil {
		return nil, platformErr(err, "Failed to list the readiness alarms")
	}
	names := map[uuid.UUID]string{}
	for _, st := range states {
		names[st.ID] = st.Name
	}
	out := map[uuid.UUID][]AlarmView{}
	for _, row := range rows {
		out[row.ReservationID] = append(out[row.ReservationID], alarmView(row.Alarm, calModel.Label{EventName: row.EventName, EventTag: row.EventTag}, names))
	}
	return out, nil
}

// GetStats is the allocated, used and free room now, per agent and per event (admin).
func (u *ResourceCalendarUseCase) GetResourceCalendarStats(ctx context.Context) (StatsView, error) {
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return StatsView{}, err
	}
	settings, err := u.store.Settings(ctx)
	if err != nil {
		return StatsView{}, platformErr(err, "Failed to read the calendar settings")
	}
	slot, _ := calModel.NewWindow(now, now.Add(time.Second))
	rs, err := u.store.ListInWindow(ctx, slot)
	if err != nil {
		return StatsView{}, platformErr(err, "Failed to read the reservations")
	}
	sortReservations(rs)
	var usage Usage
	if u.usage != nil {
		if got, uErr := u.usage.Usage(ctx, now); uErr == nil {
			usage = got
		} else {
			log.Warn().Err(uErr).Msg("Resource usage unavailable for the statistics")
		}
	}
	allocated := calModel.PeakLoad(slot, rs)
	view := StatsView{At: now, TestPool: settings.TestPool, Agents: make([]AgentStatView, 0, len(states)), Events: []EventStatView{}}
	for _, st := range states {
		a := AgentStatView{ID: st.ID, Name: st.Name, Priority: st.Priority, Used: st.Used, Connected: st.Connected, Allocated: allocated[st.ID], InUse: usage.ByAgent[st.ID]}
		if st.Used {
			a.Capacity = st.Capacity
			a.Free = st.Free(allocated[st.ID])
		}
		view.Agents = append(view.Agents, a)
	}
	ids := make([]uuid.UUID, 0, len(rs))
	for _, r := range rs {
		if r.EventID != nil {
			ids = append(ids, r.ID)
		}
	}
	labels, err := u.store.Labels(ctx, ids)
	if err != nil {
		return StatsView{}, platformErr(err, "Failed to read the event names")
	}
	conflicts := calModel.FindConflicts(slot, usedAgents(states), rs, settings.TestPool)
	inConflict := map[uuid.UUID]bool{}
	for _, c := range conflicts {
		for _, id := range c.Reservations {
			inConflict[id] = true
		}
	}
	for _, r := range rs {
		if r.EventID == nil {
			continue
		}
		used := usage.ByEvent[*r.EventID]
		view.Events = append(view.Events, EventStatView{
			ReservationID: r.ID, EventID: *r.EventID, EventName: labels[r.ID].EventName, EventTag: labels[r.ID].EventTag,
			From: r.Window.Start, To: r.Window.End, Allocated: r.Size, InUse: used,
			Free:    Amount{CPUMillicores: max(r.Size.CPUMillicores-used.CPUMillicores, 0), MemoryBytes: max(r.Size.MemoryBytes-used.MemoryBytes, 0)},
			Covered: r.Unplaced == 0 && !inConflict[r.ID],
		})
	}
	holds, err := u.store.ActiveHolds(ctx, now)
	if err != nil {
		return StatsView{}, platformErr(err, "Failed to read the test laboratory holds")
	}
	for _, h := range holds {
		if h.Via != calModel.ViaBooking {
			view.TestLabsHeld = view.TestLabsHeld.Add(h.Size)
		}
	}
	if view.PendingChangeRequests, err = u.store.CountPendingChangeRequests(ctx); err != nil {
		return StatsView{}, platformErr(err, "Failed to count the change requests")
	}
	open, err := u.store.ListAlarms(ctx, true, alarmListLimit)
	if err != nil {
		return StatsView{}, platformErr(err, "Failed to list the readiness alarms")
	}
	view.OpenAlarms = len(open)
	return view, nil
}

// GetSettings reads the calendar settings.
func (u *ResourceCalendarUseCase) GetResourceCalendarSettings(ctx context.Context) (calModel.Settings, error) {
	s, err := u.store.Settings(ctx)
	if err != nil {
		return calModel.Settings{}, platformErr(err, "Failed to read the calendar settings")
	}
	return s, nil
}

// SetTestPool sets the guaranteed minimum pool for test laboratories (admin). A pool that the reservations of the
// next month no longer leave room for is refused unless the admin allows the conflict.
func (u *ResourceCalendarUseCase) SetResourceTestPool(ctx context.Context, pool Amount, allowConflicts bool) (calModel.Settings, []ConflictView, error) {
	if pool.CPUMillicores < 0 || pool.MemoryBytes < 0 {
		return calModel.Settings{}, nil, calModel.ErrReservationInvalid.Err()
	}
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return calModel.Settings{}, nil, err
	}
	var (
		settings  calModel.Settings
		conflicts []calModel.Conflict
	)
	err = u.inTx(ctx, func(ctx context.Context, s Store) error {
		w, wErr := calModel.NewWindow(now, now.Add(30*24*time.Hour))
		if wErr != nil {
			return wErr
		}
		rs, lErr := s.ListInWindow(ctx, w)
		if lErr != nil {
			return platformErr(lErr, "Failed to read the reservations")
		}
		for _, c := range calModel.FindConflicts(w, usedAgents(states), rs, pool) {
			if c.PoolShort {
				conflicts = append(conflicts, c)
			}
		}
		if len(conflicts) > 0 && !allowConflicts {
			return calModel.ErrReservationConflict.WithContext("from", conflicts[0].Window.Start.Format(time.RFC3339)).Err()
		}
		settings = calModel.Settings{TestPool: pool, UpdatedAt: now}
		if sErr := s.SetSettings(ctx, settings); sErr != nil {
			return platformErr(sErr, "Failed to save the calendar settings")
		}
		return nil
	})
	if err != nil {
		return calModel.Settings{}, nil, err
	}
	return settings, conflictViews(conflicts), nil
}

// FutureImpact tells which future reservations lose capacity when an agent goes: the placed share of every
// reservation that has not ended (the agent deletion's confirmation lists them).
func (u *ResourceCalendarUseCase) FutureImpact(ctx context.Context, agent uuid.UUID, now time.Time) ([]infraUseCase.ReservationImpact, error) {
	rs, err := u.store.ListEndingAfter(ctx, now)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, r := range rs {
		if r.EventID != nil {
			ids = append(ids, r.ID)
		}
	}
	labels, err := u.store.Labels(ctx, ids)
	if err != nil {
		return nil, err
	}
	var out []infraUseCase.ReservationImpact
	for _, r := range rs {
		if r.EventID == nil {
			continue
		}
		units := 0
		for _, sh := range r.Placement {
			if sh.AgentID == agent {
				units += sh.Units
			}
		}
		if units == 0 {
			continue
		}
		slot := r.TeamSlot()
		out = append(out, infraUseCase.ReservationImpact{
			ReservationID: r.ID, EventID: *r.EventID, EventName: labels[r.ID].EventName,
			CPUMillicores: slot.CPUMillicores * int64(units), MemoryBytes: slot.MemoryBytes * int64(units), StartsAt: r.Window.Start, EndsAt: r.Window.End,
		})
	}
	return out, nil
}
