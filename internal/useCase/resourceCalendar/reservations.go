package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// EventReservationInput is what a platform admin sets for an event. Every pointer is optional: the plan of the
// event gives the teams and the size per team, the settings give the buffer and the tail gap, the event gives the
// window.
type EventReservationInput struct {
	// Teams and PerTeam override the plan of the event.
	Teams   *int
	PerTeam *Amount
	// BufferPercent overrides the default buffer (15%).
	BufferPercent *int
	// Dynamic is the organizer's estimate for tasks that appear later.
	Dynamic *Amount
	// TailGap is the gap after the event end; never shorter than the default (1h), the admin may set more.
	TailGap *time.Duration
	// WindowStart and WindowEnd override the window; WindowEnd is the event end, the tail gap is added to it.
	WindowStart *time.Time
	WindowEnd   *time.Time
	// AllowConflicts keeps a reservation that does not fit by packing (it is marked not covered and the admin
	// resolves it by hand); without it such a reservation is refused.
	AllowConflicts bool
	// DryRun only reports what would happen.
	DryRun bool
}

// eventWindow is the window of an event: from the stand deploy lead (plus the readiness margin) to the event end
// plus the tail gap.
func (u *ResourceCalendarUseCase) eventWindow(ctx context.Context, e eventModel.Event, in EventReservationInput, tail time.Duration) (calModel.Window, error) {
	var start, end time.Time
	if e.Lifecycle.Configured {
		lead := 30 * time.Minute
		if cfg, err := u.configs.Get(ctx, e.ID); err == nil {
			lead = time.Duration(cfg.StandTiming.DeployLeadMinutes) * time.Minute
		}
		start = e.Lifecycle.StartAt.Add(-lead - u.cfg.LeadMargin)
		if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil {
			end = *finish
		}
	}
	if in.WindowStart != nil {
		start = *in.WindowStart
	}
	if in.WindowEnd != nil {
		end = *in.WindowEnd
	}
	if start.IsZero() || end.IsZero() {
		return calModel.Window{}, calModel.ErrWindowInvalid.WithContext("reason", "the event has no schedule: set the window").Err()
	}
	return calModel.EventWindow(start, end, tail, u.cfg.TailGap)
}

// SetEventReservation creates or updates the reservation of an event (a platform admin only). Size = the plan
// per team x teams + the buffer + the organizer's estimate for dynamic tasks; the window runs from the deploy
// lead to the event end + the tail gap. The reservation is placed over the agents by packing; one that does not
// fit is refused unless the admin allows the conflict. Placed teams are never moved: a changed reservation keeps
// every team that still fits where it is.
func (u *ResourceCalendarUseCase) SetEventResourceReservation(ctx context.Context, eventID uuid.UUID, in EventReservationInput, by uuid.UUID) (ReservationResult, error) {
	now := u.now().UTC()
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return ReservationResult{}, eventModel.ErrEventNotFound.Err()
		}
		return ReservationResult{}, platformErr(err, "Failed to get the event for its reservation")
	}
	need, err := u.planner.ReservationNeed(ctx, eventID)
	if err != nil {
		return ReservationResult{}, err
	}
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return ReservationResult{}, err
	}
	var (
		result ReservationResult
		raised []alarmEvent
	)
	err = u.inTx(ctx, func(ctx context.Context, s Store) error {
		existing, getErr := s.GetEventReservation(ctx, eventID)
		if getErr != nil && !notFound(getErr) {
			return platformErr(getErr, "Failed to read the event reservation")
		}
		if notFound(getErr) {
			existing = nil
		}
		teams, perTeam, device := need.Teams, need.PerTeam, need.LargestDevice
		buffer, dynamic, tail := u.cfg.BufferPercent, Amount{}, u.cfg.TailGap
		if existing != nil {
			buffer, dynamic, tail = existing.BufferPercent, existing.Dynamic, existing.TailGap
		}
		if in.Teams != nil {
			teams = *in.Teams
		}
		if in.PerTeam != nil {
			perTeam = *in.PerTeam
		}
		if in.BufferPercent != nil {
			buffer = *in.BufferPercent
		}
		if in.Dynamic != nil {
			dynamic = *in.Dynamic
		}
		if in.TailGap != nil {
			tail = *in.TailGap
		}
		window, wErr := u.eventWindow(ctx, e, in, tail)
		if wErr != nil {
			return wErr
		}
		r := existing
		if r == nil {
			r, err = calModel.NewEventReservation(calModel.EventInput{
				EventID: eventID, Window: window, Teams: teams, PerTeam: perTeam, LargestDevice: device, BufferPercent: buffer, Dynamic: dynamic, TailGap: tail,
			}, by, now)
			if err != nil {
				return err
			}
		} else {
			if err = r.Recalculate(teams, perTeam, device, buffer, dynamic, now); err != nil {
				return err
			}
			if err = r.Reschedule(window, now); err != nil {
				return err
			}
			r.TailGap = tail
		}
		settings, setErr := s.Settings(ctx)
		if setErr != nil {
			return platformErr(setErr, "Failed to read the calendar settings")
		}
		view, conflicts, covered, planErr := u.placeAndCheck(ctx, s, r, existing != nil, states, settings.TestPool, now)
		if planErr != nil {
			return planErr
		}
		result = ReservationResult{Reservation: view, Conflicts: conflicts}
		if !covered && !in.AllowConflicts {
			ce := calModel.ErrReservationConflict.WithContext("unplaced", r.Unplaced)
			if len(conflicts) > 0 {
				ce = ce.WithContext("from", conflicts[0].From.Format(time.RFC3339))
			}
			return ce.Err()
		}
		if in.DryRun {
			return nil
		}
		if existing == nil {
			if cErr := s.CreateReservation(ctx, r); cErr != nil {
				return platformErr(cErr, "Failed to save the event reservation")
			}
		} else {
			ok, uErr := s.UpdateReservation(ctx, r)
			if uErr != nil {
				return platformErr(uErr, "Failed to save the event reservation")
			}
			if !ok {
				return calModel.ErrReservationNotFound.Err()
			}
		}
		result.Saved = true
		raised, err = u.reconcileReservation(ctx, s, r, states, now)
		return err
	})
	if err != nil {
		return ReservationResult{}, err
	}
	u.emit(ctx, raised)
	return result, nil
}

// placeAndCheck places r (keeping what already fits when it was placed before) and checks the window by
// packing; it returns the view, the conflicts that involve r (or the test pool) and whether r is covered.
func (u *ResourceCalendarUseCase) placeAndCheck(ctx context.Context, s Store, r *calModel.Reservation, keep bool, states []agentState, pool Amount, now time.Time) (ReservationView, []ConflictView, bool, error) {
	agents := usedAgents(states)
	all, err := s.ListInWindow(ctx, r.Window)
	if err != nil {
		return ReservationView{}, nil, false, platformErr(err, "Failed to read the reservations of the window")
	}
	oth := others(all, r.ID)
	if keep {
		calModel.PlaceKeeping(agents, oth, r)
	} else {
		calModel.PlaceReservation(agents, oth, r)
	}
	conflicts := involving(calModel.FindConflicts(r.Window, agents, append(append([]*calModel.Reservation(nil), oth...), r), pool), r.ID)
	view := u.reservationView(r, states, map[uuid.UUID]calModel.Label{}, nil)
	view.Covered = r.Unplaced == 0 && len(conflicts) == 0
	return view, conflictViews(conflicts), view.Covered, nil
}

// involving keeps the conflicts that concern the reservation or the test pool.
func involving(cs []calModel.Conflict, id uuid.UUID) []calModel.Conflict {
	var out []calModel.Conflict
	for _, c := range cs {
		hit := c.PoolShort
		for _, r := range c.Reservations {
			if r == id {
				hit = true
			}
		}
		if hit {
			out = append(out, c)
		}
	}
	return out
}

func conflictViews(cs []calModel.Conflict) []ConflictView {
	out := make([]ConflictView, 0, len(cs))
	for _, c := range cs {
		ids := c.Reservations
		if ids == nil {
			ids = []uuid.UUID{}
		}
		out = append(out, ConflictView{From: c.Window.Start, To: c.Window.End, ReservationIDs: ids, PoolShort: c.PoolShort, Unplaced: c.Unplaced, Short: c.Short})
	}
	return out
}

// DeleteEventReservation cancels the reservation of an event; its room is free at once and its alarms close.
func (u *ResourceCalendarUseCase) DeleteEventResourceReservation(ctx context.Context, eventID, by uuid.UUID) error {
	now := u.now().UTC()
	var closed []alarmEvent
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		r, err := s.GetEventReservation(ctx, eventID)
		if err != nil {
			if notFound(err) {
				return calModel.ErrNoReservation.Err()
			}
			return platformErr(err, "Failed to read the event reservation")
		}
		r.Cancel(now)
		if _, err = s.UpdateReservation(ctx, r); err != nil {
			return platformErr(err, "Failed to cancel the event reservation")
		}
		closed, err = u.reconcileReservation(ctx, s, r, nil, now)
		return err
	})
	if err != nil {
		return err
	}
	u.emit(ctx, closed)
	return nil
}

// ReplanReservation places a reservation again from the current agents, keeping every team that still fits:
// the admin's manual answer to an alarm (an agent is back, or a new one was added). Teams that no longer fit
// their agent are placed elsewhere; nothing that fits is moved.
func (u *ResourceCalendarUseCase) ReplanResourceReservation(ctx context.Context, id uuid.UUID, allowConflicts bool) (ReservationResult, error) {
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return ReservationResult{}, err
	}
	var (
		result ReservationResult
		raised []alarmEvent
	)
	err = u.inTx(ctx, func(ctx context.Context, s Store) error {
		r, getErr := s.GetReservation(ctx, id)
		if getErr != nil || !r.Active() {
			if getErr == nil || notFound(getErr) {
				return calModel.ErrReservationNotFound.Err()
			}
			return platformErr(getErr, "Failed to read the reservation")
		}
		settings, setErr := s.Settings(ctx)
		if setErr != nil {
			return platformErr(setErr, "Failed to read the calendar settings")
		}
		view, conflicts, covered, planErr := u.placeAndCheck(ctx, s, r, true, states, settings.TestPool, now)
		if planErr != nil {
			return planErr
		}
		result = ReservationResult{Reservation: view, Conflicts: conflicts}
		if !covered && !allowConflicts {
			return calModel.ErrReservationConflict.WithContext("unplaced", r.Unplaced).Err()
		}
		r.UpdatedAt = now
		if _, err = s.UpdateReservation(ctx, r); err != nil {
			return platformErr(err, "Failed to save the reservation")
		}
		result.Saved = true
		raised, err = u.reconcileReservation(ctx, s, r, states, now)
		return err
	})
	if err != nil {
		return ReservationResult{}, err
	}
	u.emit(ctx, raised)
	return result, nil
}

// reservationView builds the admin view of a reservation; labels and used may be empty.
func (u *ResourceCalendarUseCase) reservationView(r *calModel.Reservation, states []agentState, labels map[uuid.UUID]calModel.Label, alarms []AlarmView) ReservationView {
	names := map[uuid.UUID]string{}
	for _, s := range states {
		names[s.ID] = s.Name
	}
	v := ReservationView{
		ID: r.ID, Kind: r.Kind, EventID: r.EventID, OwnerID: r.OwnerID, From: r.Window.Start, To: r.Window.End,
		Teams: r.Teams, PerTeam: r.PerTeam, LargestDevice: r.LargestDevice, BufferPercent: r.BufferPercent, Dynamic: r.Dynamic,
		TailGap: r.TailGap, Size: r.Size, Unplaced: r.Unplaced, Placement: make([]ShareView, 0, len(r.Placement)), Alarms: alarms,
	}
	if v.Alarms == nil {
		v.Alarms = []AlarmView{}
	}
	for _, sh := range r.Placement {
		v.Placement = append(v.Placement, ShareView{AgentID: sh.AgentID, AgentName: names[sh.AgentID], Units: sh.Units})
	}
	if l, ok := labels[r.ID]; ok {
		v.EventName, v.EventTag = l.EventName, l.EventTag
	}
	return v
}

// GetEventReservation is the current reservation of an event for the admin: where it is placed, whether it is
// covered, the conflicts that involve it and its open alarms.
func (u *ResourceCalendarUseCase) GetEventResourceReservation(ctx context.Context, eventID uuid.UUID) (ReservationResult, error) {
	r, err := u.store.GetEventReservation(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return ReservationResult{}, calModel.ErrNoReservation.Err()
		}
		return ReservationResult{}, platformErr(err, "Failed to read the event reservation")
	}
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return ReservationResult{}, err
	}
	settings, err := u.store.Settings(ctx)
	if err != nil {
		return ReservationResult{}, platformErr(err, "Failed to read the calendar settings")
	}
	all, err := u.store.ListInWindow(ctx, r.Window)
	if err != nil {
		return ReservationResult{}, platformErr(err, "Failed to read the reservations of the window")
	}
	conflicts := involving(calModel.FindConflicts(r.Window, usedAgents(states), all, settings.TestPool), r.ID)
	labels, err := u.store.Labels(ctx, []uuid.UUID{r.ID})
	if err != nil {
		return ReservationResult{}, platformErr(err, "Failed to read the event name")
	}
	alarms, err := u.openAlarmsByReservation(ctx, states)
	if err != nil {
		return ReservationResult{}, err
	}
	view := u.reservationView(r, states, labels, alarms[r.ID])
	view.Covered = r.Unplaced == 0 && len(conflicts) == 0
	if u.usage != nil {
		if usage, uErr := u.usage.Usage(ctx, now); uErr == nil {
			view.Used = usage.ByEvent[eventID]
		}
	}
	return ReservationResult{Reservation: view, Conflicts: conflictViews(conflicts), Saved: true}, nil
}
