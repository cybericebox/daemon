package resourceCalendarUseCase

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// The calendar resolves the reservation a group or lab runs under for the fleet.
var _ infraModel.PartResolver = (*ResourceCalendarUseCase)(nil)

// Resolve finds the agent and the part a group or a lab runs under. For a calendar reservation the unit (a team, a
// preview owner, the booking) is assigned to a share's agent once and stays there; a group that already lives on an
// agent (pinned) keeps it. A part that is not on its agent yet is sent once; still missing is
// ErrNoClusterReservation, which the callers wait on.
func (u *ResourceCalendarUseCase) Resolve(ctx context.Context, ref infraModel.ReservationRef, pinned *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	switch {
	case ref.TestDeployID != nil:
		return u.resolveRun(ctx, *ref.TestDeployID, pinned)
	case ref.OwnerID != nil:
		return u.resolveTestGroup(ctx, *ref.OwnerID, pinned)
	case ref.CalendarID != nil || ref.EventID != nil:
		return u.resolveUnit(ctx, ref, pinned)
	}
	return uuid.Nil, uuid.Nil, infraModel.ErrNoClusterReservation
}

// resolveUnit places the unit of a calendar reservation and finds the part on its agent.
func (u *ResourceCalendarUseCase) resolveUnit(ctx context.Context, ref infraModel.ReservationRef, pinned *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	now := u.now().UTC()
	var calendarID, agent uuid.UUID
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		var r *calModel.Reservation
		var err error
		if ref.CalendarID != nil {
			r, err = s.GetReservation(ctx, *ref.CalendarID)
		} else {
			r, err = s.GetEventReservation(ctx, *ref.EventID)
		}
		if err != nil {
			if notFound(err) {
				return infraModel.ErrNoClusterReservation
			}
			return platformErr(err, "Failed to read the reservation of a lab group")
		}
		if !r.Active() {
			return infraModel.ErrNoClusterReservation
		}
		units, err := s.Units(ctx, r.ID)
		if err != nil {
			return platformErr(err, "Failed to read the units of a reservation")
		}
		// A group that lives on an agent without a part of this reservation (made before the reservations, or by
		// the old placement) does not pin its unit there: the unit follows the placement, and the fleet refuses the
		// mismatch, so such a group is recreated.
		if pinned != nil {
			parts, pErr := s.PartsOfCalendar(ctx, r.ID)
			if pErr != nil {
				return platformErr(pErr, "Failed to read the cluster reservation parts")
			}
			if !hasPartOn(parts, *pinned) {
				pinned = nil
			}
		}
		a, placed := units[ref.Unit]
		switch {
		case placed && pinned != nil && a != *pinned:
			// The group lives elsewhere and never moves: its part there is used, if it has one.
			log.Warn().Err(infraModel.ErrPlacementMismatch).Str("reservation_id", r.ID.String()).Str("unit", ref.Unit).
				Str("unit_agent", a.String()).Str("group_agent", pinned.String()).Msg("A lab group is not on the agent of its unit")
			a = *pinned
		case !placed:
			if pinned != nil {
				a = *pinned
			} else if a, err = calModel.AssignUnit(r.Placement, units, ref.Unit); err != nil {
				return fmt.Errorf("%w: %v", infraModel.ErrNoClusterReservation, err)
			}
			if a, err = s.AssignUnit(ctx, r.ID, ref.Unit, a, now); err != nil {
				return platformErr(err, "Failed to place a unit of a reservation")
			}
		}
		calendarID, agent = r.ID, a
		return nil
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	find := func() (calModel.Part, bool, error) {
		parts, err := u.store.PartsOfCalendar(ctx, calendarID)
		if err != nil {
			return calModel.Part{}, false, platformErr(err, "Failed to read the cluster reservation parts")
		}
		for _, p := range parts {
			if p.AgentID == agent {
				return p, true, nil
			}
		}
		return calModel.Part{}, false, nil
	}
	part, err := u.livePart(ctx, agent, find, func() error { return u.SyncCalendarReservation(ctx, calendarID) })
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return agent, part.ID, nil
}

// resolveRun finds the part of a test run: its own run part, or, for a run admitted inside the author's booking, the
// booking's part.
func (u *ResourceCalendarUseCase) resolveRun(ctx context.Context, deploy uuid.UUID, pinned *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	holds, err := u.store.ActiveHolds(ctx, u.now().UTC())
	if err != nil {
		return uuid.Nil, uuid.Nil, platformErr(err, "Failed to read the test laboratory holds")
	}
	for _, h := range holds {
		if h.ID == deploy && h.Via == calModel.ViaBooking && h.ReservationID != nil {
			id := *h.ReservationID
			return u.resolveUnit(ctx, infraModel.ReservationRef{CalendarID: &id, Unit: calModel.UnitBooking}, pinned)
		}
	}
	find := func() (calModel.Part, bool, error) {
		p, err := u.store.RunPart(ctx, deploy)
		if err != nil {
			if notFound(err) {
				return calModel.Part{}, false, nil
			}
			return calModel.Part{}, false, platformErr(err, "Failed to read the part of a test run")
		}
		return p, true, nil
	}
	return u.resolveOwnPart(ctx, find, pinned)
}

// resolveTestGroup finds the part of an author's test group.
func (u *ResourceCalendarUseCase) resolveTestGroup(ctx context.Context, owner uuid.UUID, pinned *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	find := func() (calModel.Part, bool, error) {
		p, err := u.store.GroupPart(ctx, owner)
		if err != nil {
			if notFound(err) {
				return calModel.Part{}, false, nil
			}
			return calModel.Part{}, false, platformErr(err, "Failed to read the part of a test group")
		}
		return p, true, nil
	}
	return u.resolveOwnPart(ctx, find, pinned)
}

// resolveOwnPart resolves a part that names its own agent (a test run, a test group).
func (u *ResourceCalendarUseCase) resolveOwnPart(ctx context.Context, find func() (calModel.Part, bool, error), pinned *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	p, ok, err := find()
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, uuid.Nil, infraModel.ErrNoClusterReservation
	}
	if pinned != nil && p.AgentID != *pinned {
		return uuid.Nil, uuid.Nil, infraModel.ErrPlacementMismatch
	}
	part, err := u.livePart(ctx, p.AgentID, find, func() error { return u.SyncAgent(ctx, p.AgentID) })
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return part.AgentID, part.ID, nil
}

// hasPartOn reports a live part of the reservation on the agent.
func hasPartOn(parts []calModel.Part, agent uuid.UUID) bool {
	for _, p := range parts {
		if p.AgentID == agent && !p.Retired() {
			return true
		}
	}
	return false
}

// livePart returns the part once the agent holds it: a part that is missing or was never confirmed is sent once. A
// part that ended, or whose window has passed, takes no new work: ErrReservationEnded, which callers treat as closed
// (a wait for stands), never as a failure.
func (u *ResourceCalendarUseCase) livePart(ctx context.Context, agent uuid.UUID, find func() (calModel.Part, bool, error), send func() error) (calModel.Part, error) {
	p, ok, err := find()
	if err != nil {
		return calModel.Part{}, err
	}
	if ok && (p.Status.Phase == calModel.PartEnded || !p.Window.End.After(u.now().UTC())) {
		return calModel.Part{}, infraModel.ErrReservationEnded
	}
	if ok && p.SyncedRev > 0 {
		return p, nil
	}
	if sendErr := send(); sendErr != nil {
		log.Warn().Err(sendErr).Str("agent_id", agent.String()).Msg("Failed to send a cluster reservation a lab group needs")
	}
	if p, ok, err = find(); err != nil {
		return calModel.Part{}, err
	}
	if !ok || p.SyncedRev == 0 {
		return calModel.Part{}, infraModel.ErrNoClusterReservation
	}
	return p, nil
}
