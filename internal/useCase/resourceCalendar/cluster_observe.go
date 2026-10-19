package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// Ending reasons the agent gives.
const (
	EndingWindowClosing = "WindowClosing"
	EndingTenantGone    = "TenantGone"
	EndingAmountReduced = "AmountReduced"
)

// driftTolerance is how far the agent's end may be from the desired one before it counts as drift.
const driftTolerance = time.Second

type (
	// PartListener acts on the ending and the end of a part: a test run's laboratory, an event's labs on an agent.
	// It is told once per part and target phase.
	PartListener interface {
		PartEnding(ctx context.Context, n PartNotice)
		PartEnded(ctx context.Context, n PartNotice)
	}

	// ReservationNotifier tells people about an event reservation that ends while the event runs, or that was made
	// smaller than what runs in it (the inbox request router).
	ReservationNotifier interface {
		ReservationEnding(ctx context.Context, n ReservationNotice) error
		ReservationReduced(ctx context.Context, n ReservationNotice) error
	}

	// PartNotice is a part with what it belongs to.
	PartNotice struct {
		Part calModel.Part
		// EventID is the event of a calendar part (nil for a booking or a test part); EventEnded says the event is
		// over (the part is in its tail gap).
		EventID    *uuid.UUID
		EventEnded bool
		EndsAt     time.Time
		Reason     string
		// At is when the calendar acts on it (its clock).
		At time.Time
	}

	// ReservationNotice is an event reservation part people hear about.
	ReservationNotice = inboxUseCase.ReservationNotice
)

// SetPartListener wires what acts on the ending and the end of parts (the test labs, the event stands).
func (u *ResourceCalendarUseCase) SetPartListener(l PartListener) { u.cluster.listener = l }

// statusOf maps the agent's report of a reservation onto the part status.
func statusOf(r infraModel.ClusterReservation, observedAt time.Time) calModel.PartStatus {
	st := calModel.PartStatus{ObservedAt: &observedAt}
	if r.Status == nil {
		return st
	}
	st.Phase, st.NoticeAt, st.EndsAt, st.EndingReason = r.Status.Phase, r.Status.NoticeAt, r.Status.EndsAt, r.Status.EndingReason
	st.Used, st.Groups, st.Exceeded = r.Status.Used, r.Status.Groups, r.Status.Exceeded
	if !r.Status.ObservedAt.IsZero() {
		at := r.Status.ObservedAt
		st.ObservedAt = &at
	}
	return st
}

// ObserveClusterReservations takes what an agent's monitoring says about its reservations: a snapshot replaces the
// observed state of the agent's parts (a desired part it does not list is sent again), a delta updates the listed
// ones, and a deleted key is the end of its part. The ending notice and the end act once per part and phase:
//   - an end earlier than the desired one is drift: the part is sent again at once, nobody is told;
//   - a test run's notice and end go to the test labs (the author is told, the lab ends like at its lease);
//   - an event part's notice while the event still runs tells the admins and the organizers (the window is too
//     short); after the event it is the expected end of the tail gap;
//   - a part made smaller than what runs in it tells the admins;
//   - the end of an event part releases its labs without a failure.
func (u *ResourceCalendarUseCase) ObserveClusterReservations(ctx context.Context, agentID uuid.UUID, snapshot bool, rs []infraModel.ClusterReservation, deleted []uuid.UUID) error {
	now := u.now().UTC()
	parts, err := u.store.PartsOfAgent(ctx, agentID)
	if err != nil {
		return platformErr(err, "Failed to read the cluster reservation parts")
	}
	byID := make(map[uuid.UUID]calModel.Part, len(parts))
	for _, p := range parts {
		byID[p.ID] = p
	}
	resync := false
	seen := map[uuid.UUID]bool{}
	for _, r := range rs {
		p, ok := byID[r.ID]
		if !ok {
			continue // an orphan: the reconcile deletes it
		}
		seen[p.ID] = true
		next := statusOf(r, now)
		// An end earlier than the desired one is drift: the part is sent again, its notice is not recorded.
		if drifted(p, next, r) {
			resync = true
			continue
		}
		// The end is recorded by the transition, once.
		if next.Phase != calModel.PartEnded {
			if err = u.store.Observe(ctx, p.ID, next); err != nil {
				return platformErr(err, "Failed to record the status of a cluster reservation")
			}
		}
		u.transition(ctx, p, next, r, now)
	}
	if snapshot {
		for _, p := range parts {
			if !seen[p.ID] && !p.Retired() && p.Window.End.After(now) {
				resync = true // a desired part the agent does not hold
			}
		}
	}
	for _, id := range deleted {
		p, ok := byID[id]
		if !ok || p.Status.Phase == calModel.PartEnded {
			continue // an end already acted on
		}
		if err = u.finishPart(ctx, p, now); err != nil {
			log.Warn().Err(err).Str("part_id", p.ID.String()).Msg("Failed to act on the end of a cluster reservation part")
		}
	}
	if resync {
		if err = u.SyncAgent(ctx, agentID); err != nil {
			log.Warn().Err(err).Str("agent_id", agentID.String()).Msg("Failed to send the cluster reservations again")
		}
	}
	return nil
}

// drifted reports an ending notice for an end earlier than the desired one (an update the agent missed).
func drifted(p calModel.Part, next calModel.PartStatus, r infraModel.ClusterReservation) bool {
	return next.Phase == calModel.PartEnding && p.Status.Phase != calModel.PartEnding && next.EndingReason == EndingWindowClosing &&
		!p.Retired() && p.Window.End.After(r.To.Add(driftTolerance))
}

// transition acts once on a change of a part's phase: the ending notice, a reduced amount, the end.
func (u *ResourceCalendarUseCase) transition(ctx context.Context, p calModel.Part, next calModel.PartStatus, r infraModel.ClusterReservation, now time.Time) {
	prev := p.Status
	if next.Phase == calModel.PartEnding && prev.Phase != calModel.PartEnding && !p.Retired() {
		u.ending(ctx, p, next, r.To, now)
	}
	if next.Exceeded && !prev.Exceeded && !p.Retired() {
		u.reduced(ctx, p, next, now)
	}
	if next.Phase == calModel.PartEnded && prev.Phase != calModel.PartEnded {
		if err := u.finishWith(ctx, p, next, now); err != nil {
			log.Warn().Err(err).Str("part_id", p.ID.String()).Msg("Failed to act on the end of a cluster reservation part")
		}
	}
}

// notice builds what the listeners and the notifier need about a part.
func (u *ResourceCalendarUseCase) notice(ctx context.Context, p calModel.Part, endsAt time.Time, reason string, now time.Time) (PartNotice, *ReservationNotice) {
	n := PartNotice{Part: p, EndsAt: endsAt, Reason: reason, At: now}
	if p.CalendarID == nil || u.events == nil {
		return n, nil
	}
	r, err := u.store.GetReservation(ctx, *p.CalendarID)
	if err != nil || r.EventID == nil {
		return n, nil
	}
	n.EventID = r.EventID
	e, err := u.events.GetByID(ctx, *r.EventID)
	if err != nil {
		return n, nil
	}
	if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && !finish.After(now) {
		n.EventEnded = true
	}
	return n, &ReservationNotice{ReservationID: r.ID, EventID: e.ID, EventName: e.Name, EventTag: e.Tag, AgentID: p.AgentID, EndsAt: endsAt}
}

func (u *ResourceCalendarUseCase) ending(ctx context.Context, p calModel.Part, st calModel.PartStatus, to, now time.Time) {
	endsAt := to
	if st.EndsAt != nil {
		endsAt = *st.EndsAt
	}
	n, rn := u.notice(ctx, p, endsAt, st.EndingReason, now)
	if rn != nil && !n.EventEnded {
		if notifier, ok := u.notifier.(ReservationNotifier); ok {
			if err := notifier.ReservationEnding(ctx, *rn); err != nil {
				log.Warn().Err(err).Str("part_id", p.ID.String()).Msg("Failed to notify about an ending reservation")
			}
		}
	}
	if u.cluster.listener != nil {
		u.cluster.listener.PartEnding(ctx, n)
	}
}

func (u *ResourceCalendarUseCase) reduced(ctx context.Context, p calModel.Part, st calModel.PartStatus, now time.Time) {
	endsAt := now
	if st.EndsAt != nil {
		endsAt = *st.EndsAt
	}
	_, rn := u.notice(ctx, p, endsAt, EndingAmountReduced, now)
	if rn == nil {
		return
	}
	if notifier, ok := u.notifier.(ReservationNotifier); ok {
		if err := notifier.ReservationReduced(ctx, *rn); err != nil {
			log.Warn().Err(err).Str("part_id", p.ID.String()).Msg("Failed to notify about a reduced reservation")
		}
	}
}

func (u *ResourceCalendarUseCase) ended(ctx context.Context, p calModel.Part, now time.Time) {
	n, _ := u.notice(ctx, p, now, "", now)
	if u.cluster.listener != nil {
		u.cluster.listener.PartEnded(ctx, n)
	}
}

// UnitReservation is where the reservation of one unit of an event stands: EndsAt while its part gives the ending
// notice, Ended once the part ended.
type UnitReservation struct {
	EndsAt *time.Time
	Ended  bool
}

// EventUnitReservations is the reservation state of every placed unit of an event (by unit key: TeamUnit), for the
// staff view of the stands. Units on a part that is neither ending nor ended are left out.
func (u *ResourceCalendarUseCase) EventUnitReservations(ctx context.Context, eventID uuid.UUID) (map[string]UnitReservation, error) {
	r, err := u.store.GetEventReservation(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return map[string]UnitReservation{}, nil
		}
		return nil, platformErr(err, "Failed to read the event reservation")
	}
	units, err := u.store.Units(ctx, r.ID)
	if err != nil {
		return nil, platformErr(err, "Failed to read the units of the reservation")
	}
	parts, err := u.store.PartsOfCalendars(ctx, []uuid.UUID{r.ID})
	if err != nil {
		return nil, platformErr(err, "Failed to read the cluster reservation parts")
	}
	byAgent := map[uuid.UUID]calModel.Part{}
	for _, p := range parts {
		if !p.Retired() {
			byAgent[p.AgentID] = p
		}
	}
	out := map[string]UnitReservation{}
	for unit, agent := range units {
		p, ok := byAgent[agent]
		if !ok {
			continue
		}
		switch p.Status.Phase {
		case calModel.PartEnded:
			out[unit] = UnitReservation{Ended: true}
		case calModel.PartEnding:
			endsAt := p.Window.End
			if p.Status.EndsAt != nil {
				endsAt = *p.Status.EndsAt
			}
			out[unit] = UnitReservation{EndsAt: &endsAt}
		}
	}
	return out, nil
}
