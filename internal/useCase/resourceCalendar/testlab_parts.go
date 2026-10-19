package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// fallbackGroupOverhead is the group part of an author's test group while no agent has reported how it sizes the
// group's pods (the VPN of one user and one gateway): the agent's chart defaults.
var fallbackGroupOverhead = Amount{CPUMillicores: 500, MemoryBytes: 512 << 20}

// exactWindow is a run's window at the agent's precision (milliseconds); a test run is not slot aligned.
func exactWindow(from, to time.Time) calModel.Window {
	return calModel.Window{Start: from.UTC().Truncate(time.Millisecond), End: to.UTC().Truncate(time.Millisecond)}
}

// groupOverhead is the amount of an author's group part: its own pods, by the agents' formula.
func (u *ResourceCalendarUseCase) groupOverhead() Amount {
	if u.overhead != nil {
		if o := u.overhead(); o.CPUMillicores > 0 && o.MemoryBytes > 0 {
			return o
		}
	}
	return fallbackGroupOverhead
}

// testAgent picks the agent of a test laboratory admitted by the pool or free room: the agent of the author's
// group when it has one (a run never needs a second agent), else the first agent, in placement order, that allows
// the largest device and has the room over the lease, else the first that allows the device.
func testAgent(agents []calModel.Agent, rs []*calModel.Reservation, w calModel.Window, size, device Amount, pinned *uuid.UUID) (uuid.UUID, bool) {
	if pinned != nil {
		return *pinned, true
	}
	peak := calModel.PeakLoad(w, rs)
	var fallback *uuid.UUID
	for _, a := range agents {
		if !a.Allows(device) {
			continue
		}
		if fallback == nil {
			id := a.ID
			fallback = &id
		}
		a.Capacity = a.CapacityOver(w)
		if size.Within(a.Free(peak[a.ID])) {
			return a.ID, true
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return uuid.Nil, false
}

// testRunParts records the parts of an admitted test laboratory (the calendar lock is held): a run admitted by the
// pool or free room gets its own run part on agent, sized as its lab; a run inside a booking runs under the booking's
// part. Either way the author's group part on that agent covers every run of the author.
func (u *ResourceCalendarUseCase) testRunParts(ctx context.Context, s Store, req TestLabRequest, via string, agent uuid.UUID, lease time.Duration, now time.Time) (uuid.UUID, error) {
	var runPart uuid.UUID
	if via != calModel.ViaBooking {
		p := calModel.NewPart(u.newPartID(), agent, calModel.PartTestRun, exactWindow(now, now.Add(lease)), req.Size, now)
		deploy, owner := req.ID, req.Owner
		p.TestDeployID, p.OwnerID = &deploy, &owner
		if p.Amount.CPUMillicores <= 0 || p.Amount.MemoryBytes <= 0 {
			p.Amount = p.Amount.Max(Amount{CPUMillicores: 1, MemoryBytes: 1})
		}
		if err := s.SavePart(ctx, p); err != nil {
			return uuid.Nil, platformErr(err, "Failed to save the part of a test run")
		}
		runPart = p.ID
	}
	return runPart, u.fitGroupPart(ctx, s, req.Owner, &agent, now)
}

// fitGroupPart makes the author's group part cover every run the author has: from the earliest start to the latest
// end of the author's test laboratories (not previews). Without runs the part is retired. agent is where a new
// group part goes (nil: none is made).
func (u *ResourceCalendarUseCase) fitGroupPart(ctx context.Context, s Store, owner uuid.UUID, agent *uuid.UUID, now time.Time) error {
	holds, err := s.ActiveHolds(ctx, now)
	if err != nil {
		return platformErr(err, "Failed to read the test laboratory holds")
	}
	var from, to time.Time
	for _, h := range holds {
		if h.OwnerID != owner || h.EventID != nil {
			continue
		}
		if from.IsZero() || h.StartsAt.Before(from) {
			from = h.StartsAt
		}
		if h.ExpiresAt.After(to) {
			to = h.ExpiresAt
		}
	}
	current, err := s.GroupPart(ctx, owner)
	exists := err == nil
	if err != nil && !notFound(err) {
		return platformErr(err, "Failed to read the part of a test group")
	}
	switch {
	case to.IsZero() && exists:
		current.Retire(now)
		return s.SavePart(ctx, current)
	case to.IsZero():
		return nil
	case exists:
		w := exactWindow(from, to)
		if current.Window.Start.Before(w.Start) {
			w.Start = current.Window.Start // a started part keeps its start
		}
		current.SetDesired(w, current.Amount.Max(u.groupOverhead()), now)
		return s.SavePart(ctx, current)
	case agent != nil:
		p := calModel.NewPart(u.newPartID(), *agent, calModel.PartTestGroup, exactWindow(from, to), u.groupOverhead(), now)
		o := owner
		p.OwnerID = &o
		return s.SavePart(ctx, p)
	}
	return nil
}

// extendRunPart moves the end of a run's part to the new end of its lease (the id stays) and the group part with it.
func (u *ResourceCalendarUseCase) extendRunPart(ctx context.Context, s Store, id, owner uuid.UUID, until, now time.Time) error {
	p, err := s.RunPart(ctx, id)
	switch {
	case err == nil:
		w := p.Window
		w.End = until.UTC().Truncate(time.Millisecond)
		if w.End.After(p.Window.End) {
			p.SetDesired(w, p.Amount, now)
			if err = s.SavePart(ctx, p); err != nil {
				return platformErr(err, "Failed to extend the part of a test run")
			}
		}
	case !notFound(err):
		return platformErr(err, "Failed to read the part of a test run")
	}
	return u.fitGroupPart(ctx, s, owner, nil, now)
}

// endRunPart retires the part of a run that ended and shrinks the group part to the runs that are left (retired with
// the last one).
func (u *ResourceCalendarUseCase) endRunPart(ctx context.Context, s Store, id uuid.UUID, owner *uuid.UUID, now time.Time) error {
	p, err := s.RunPart(ctx, id)
	switch {
	case err == nil:
		p.Retire(now)
		if err = s.SavePart(ctx, p); err != nil {
			return platformErr(err, "Failed to retire the part of a test run")
		}
		if owner == nil {
			owner = p.OwnerID
		}
	case !notFound(err):
		return platformErr(err, "Failed to read the part of a test run")
	}
	if owner == nil {
		return nil
	}
	return u.fitGroupPart(ctx, s, *owner, nil, now)
}

// testRunAgent is the agent the run goes to and, for a booking, the part it runs under.
func (u *ResourceCalendarUseCase) testRunAgent(ctx context.Context, s Store, a admission, states []agentState, size, device Amount, lease time.Duration, pinned *uuid.UUID, now time.Time) (uuid.UUID, error) {
	if a.via == calModel.ViaBooking && a.reservation != nil {
		units, err := s.Units(ctx, *a.reservation)
		if err != nil {
			return uuid.Nil, platformErr(err, "Failed to read the units of a booking")
		}
		if agent, ok := units[calModel.UnitBooking]; ok {
			return agent, nil
		}
		b, err := s.GetReservation(ctx, *a.reservation)
		if err != nil {
			return uuid.Nil, platformErr(err, "Failed to read the booking")
		}
		agent := uuid.Nil
		if pinned != nil {
			agent = *pinned
		} else if agent, err = calModel.AssignUnit(b.Placement, units, calModel.UnitBooking); err != nil {
			return uuid.Nil, infraModel.ErrNoClusterReservation
		}
		return s.AssignUnit(ctx, b.ID, calModel.UnitBooking, agent, now)
	}
	w, err := calModel.NewWindow(now, now.Add(lease))
	if err != nil {
		return uuid.Nil, calModel.ErrBookingInvalid.Err()
	}
	rs, err := s.ListInWindow(ctx, w)
	if err != nil {
		return uuid.Nil, platformErr(err, "Failed to read the reservations")
	}
	agent, ok := testAgent(usedAgents(states), rs, w, size, device, pinned)
	if !ok {
		return uuid.Nil, noRoom(admission{}, lease)
	}
	return agent, nil
}
