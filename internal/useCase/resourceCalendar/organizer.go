package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// ChangeInput is an organizer's change request: the size, the window and/or the estimate for future dynamic
// tasks, with a reason. Extending the existing reservation is the way; the platform admin decides.
type ChangeInput struct {
	// Size is the wanted total (CPU and memory).
	Size *Amount
	// Dynamic is the wanted estimate for future dynamic tasks.
	Dynamic *Amount
	// WindowStart and WindowEnd are the wanted window; the tail gap is added to the end when approved.
	WindowStart *time.Time
	WindowEnd   *time.Time
	Reason      string
}

func organizerChange(c calModel.ChangeRequest) OrganizerChangeView {
	return OrganizerChangeView{
		ID: c.ID, RequestedAt: c.RequestedAt, Size: c.Size, Dynamic: c.Dynamic, WindowStart: c.WindowStart, WindowEnd: c.WindowEnd,
		Reason: c.Reason, Status: c.Status, DecidedAt: c.DecidedAt, DecisionNote: c.DecisionNote,
	}
}

// GetEventResources is what an organizer sees: allocated vs used, the window, whether the reservation holds and
// the change requests. Never an agent.
func (u *ResourceCalendarUseCase) GetEventResources(ctx context.Context, eventID uuid.UUID) (OrganizerReservationView, error) {
	changes, err := u.store.ListChangeRequests(ctx, nil, &eventID)
	if err != nil {
		return OrganizerReservationView{}, platformErr(err, "Failed to list the change requests")
	}
	view := OrganizerReservationView{Changes: make([]OrganizerChangeView, 0, len(changes))}
	for _, c := range changes {
		view.Changes = append(view.Changes, organizerChange(c.ChangeRequest))
	}
	r, err := u.store.GetEventReservation(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return view, nil
		}
		return OrganizerReservationView{}, platformErr(err, "Failed to read the event reservation")
	}
	now := u.now().UTC()
	view.Reserved = true
	view.From, view.To, view.Teams, view.Allocated = r.Window.Start, r.Window.End, r.Teams, r.Size
	view.BufferPercent, view.Dynamic = r.BufferPercent, r.Dynamic
	if u.usage != nil {
		if usage, uErr := u.usage.Usage(ctx, now); uErr == nil {
			view.InUse = usage.ByEvent[eventID]
		} else {
			log.Warn().Err(uErr).Msg("Resource usage unavailable for the organizer view")
		}
	}
	view.Free = Amount{CPUMillicores: max(r.Size.CPUMillicores-view.InUse.CPUMillicores, 0), MemoryBytes: max(r.Size.MemoryBytes-view.InUse.MemoryBytes, 0)}
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return OrganizerReservationView{}, err
	}
	view.Covered = u.covered(ctx, r, states)
	return view, nil
}

// covered reports whether a reservation is placed in full and no slot of its window is over capacity.
func (u *ResourceCalendarUseCase) covered(ctx context.Context, r *calModel.Reservation, states []agentState) bool {
	if r.Unplaced > 0 {
		return false
	}
	all, err := u.store.ListInWindow(ctx, r.Window)
	if err != nil {
		return false
	}
	settings, err := u.store.Settings(ctx)
	if err != nil {
		return false
	}
	return len(involving(calModel.FindConflicts(r.Window, usedAgents(states), all, settings.TestPool), r.ID)) == 0
}

// RequestResourceChange sends the platform admin a request to change the event's reservation. One request waits
// at a time.
func (u *ResourceCalendarUseCase) RequestResourceChange(ctx context.Context, eventID, by uuid.UUID, in ChangeInput) (OrganizerChangeView, error) {
	now := u.now().UTC()
	var created *calModel.ChangeRequest
	var label calModel.Label
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		r, err := s.GetEventReservation(ctx, eventID)
		if err != nil {
			if notFound(err) {
				return calModel.ErrNoReservation.Err()
			}
			return platformErr(err, "Failed to read the event reservation")
		}
		pending := calModel.ChangePending
		waiting, err := s.ListChangeRequests(ctx, &pending, &eventID)
		if err != nil {
			return platformErr(err, "Failed to list the change requests")
		}
		if len(waiting) > 0 {
			return calModel.ErrChangeRequestPending.Err()
		}
		c, err := calModel.NewChangeRequest(r, by, in.Size, in.Dynamic, in.WindowStart, in.WindowEnd, in.Reason, now)
		if err != nil {
			return err
		}
		if err = s.CreateChangeRequest(ctx, c); err != nil {
			return platformErr(err, "Failed to save the change request")
		}
		created = c
		if labels, lErr := s.Labels(ctx, []uuid.UUID{r.ID}); lErr == nil {
			label = labels[r.ID]
		}
		return nil
	})
	if err != nil {
		return OrganizerChangeView{}, err
	}
	if u.notifier != nil {
		if nErr := u.notifier.ResourceChangeRequested(ctx, u.changeNotice(*created, label, by)); nErr != nil {
			log.Warn().Err(nErr).Msg("Failed to notify the admins about the change request")
		}
	}
	return organizerChange(*created), nil
}

func (u *ResourceCalendarUseCase) changeNotice(c calModel.ChangeRequest, l calModel.Label, requester uuid.UUID) inboxUseCase.ResourceChange {
	n := inboxUseCase.ResourceChange{
		ID: c.ID, EventID: c.EventID, EventName: l.EventName, EventTag: l.EventTag, RequestedBy: requester, RequestedAt: c.RequestedAt,
		Reason: c.Reason, DecisionNote: c.DecisionNote, Summary: changeSummary(c),
	}
	return n
}

// changeSummary is the request as text ("size 4000m / 8Gi; until 2026-10-05 18:00 UTC").
func changeSummary(c calModel.ChangeRequest) string {
	var parts []string
	if c.Size != nil {
		parts = append(parts, "size "+amountText(*c.Size))
	}
	if c.Dynamic != nil {
		parts = append(parts, "dynamic tasks "+amountText(*c.Dynamic))
	}
	if c.WindowStart != nil {
		parts = append(parts, "from "+c.WindowStart.UTC().Format("2006-01-02 15:04")+" UTC")
	}
	if c.WindowEnd != nil {
		parts = append(parts, "until "+c.WindowEnd.UTC().Format("2006-01-02 15:04")+" UTC")
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}

func amountText(a Amount) string {
	return formatMillicores(a.CPUMillicores) + " / " + formatBytes(a.MemoryBytes)
}

// ListChangeRequests lists the change requests for the admin, newest first; status and event narrow it.
func (u *ResourceCalendarUseCase) ListResourceChangeRequests(ctx context.Context, status *calModel.ChangeStatus, eventID *uuid.UUID) ([]ChangeRequestView, error) {
	rows, err := u.store.ListChangeRequests(ctx, status, eventID)
	if err != nil {
		return nil, platformErr(err, "Failed to list the change requests")
	}
	out := make([]ChangeRequestView, 0, len(rows))
	for _, row := range rows {
		out = append(out, u.changeView(ctx, row))
	}
	return out, nil
}

func (u *ResourceCalendarUseCase) changeView(ctx context.Context, row calModel.NamedChangeRequest) ChangeRequestView {
	c := row.ChangeRequest
	v := ChangeRequestView{
		ID: c.ID, ReservationID: c.ReservationID, EventID: c.EventID, EventName: row.EventName, EventTag: row.EventTag,
		RequestedBy: c.RequestedBy, RequestedAt: c.RequestedAt, Size: c.Size, Dynamic: c.Dynamic, WindowStart: c.WindowStart, WindowEnd: c.WindowEnd,
		Reason: c.Reason, Status: c.Status, DecidedBy: c.DecidedBy, DecidedAt: c.DecidedAt, DecisionNote: c.DecisionNote,
	}
	if r, err := u.store.GetReservation(ctx, c.ReservationID); err == nil {
		v.CurrentSize, v.CurrentFrom, v.CurrentTo = r.Size, r.Window.Start, r.Window.End
	}
	return v
}

// DecideChangeRequest approves or rejects a change request (a platform admin only). Approving extends the
// existing reservation (size, estimate, window) and places it again, keeping every team that still fits; if it
// no longer fits by packing it is refused unless the admin allows the conflict. Nothing is moved automatically.
func (u *ResourceCalendarUseCase) DecideResourceChangeRequest(ctx context.Context, id uuid.UUID, approve bool, note string, by uuid.UUID, allowConflicts bool) (ChangeRequestView, error) {
	now := u.now().UTC()
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return ChangeRequestView{}, err
	}
	var (
		decided calModel.ChangeRequest
		label   calModel.Label
		raised  []alarmEvent
	)
	err = u.inTx(ctx, func(ctx context.Context, s Store) error {
		c, gErr := s.GetChangeRequest(ctx, id)
		if gErr != nil {
			if notFound(gErr) {
				return calModel.ErrChangeRequestNotFound.Err()
			}
			return platformErr(gErr, "Failed to read the change request")
		}
		if dErr := c.Decide(approve, by, note, now); dErr != nil {
			return dErr
		}
		if approve {
			r, rErr := s.GetReservation(ctx, c.ReservationID)
			if rErr != nil || !r.Active() {
				return calModel.ErrReservationNotFound.Err()
			}
			if aErr := u.applyChange(r, *c, now); aErr != nil {
				return aErr
			}
			settings, setErr := s.Settings(ctx)
			if setErr != nil {
				return platformErr(setErr, "Failed to read the calendar settings")
			}
			_, conflicts, covered, pErr := u.placeAndCheck(ctx, s, r, true, states, settings.TestPool, now)
			if pErr != nil {
				return pErr
			}
			if !covered && !allowConflicts {
				ce := calModel.ErrReservationConflict.WithPublicContext("unplaced", r.Unplaced)
				if len(conflicts) > 0 {
					ce = ce.WithPublicContext("from", conflicts[0].From.Format(time.RFC3339))
				}
				return ce.Err()
			}
			ok, uErr := s.UpdateReservation(ctx, r)
			if uErr != nil {
				return platformErr(uErr, "Failed to save the reservation")
			}
			if !ok {
				return calModel.ErrReservationNotFound.Err()
			}
			raised, uErr = u.reconcileReservation(ctx, s, r, states, now)
			if uErr != nil {
				return uErr
			}
		}
		ok, dErr := s.DecideChangeRequest(ctx, c)
		if dErr != nil {
			return platformErr(dErr, "Failed to save the decision")
		}
		if !ok {
			return calModel.ErrChangeRequestDecided.Err()
		}
		decided = *c
		if labels, lErr := s.Labels(ctx, []uuid.UUID{c.ReservationID}); lErr == nil {
			label = labels[c.ReservationID]
		}
		return nil
	})
	if err != nil {
		return ChangeRequestView{}, err
	}
	u.emit(ctx, raised)
	if u.notifier != nil {
		if nErr := u.notifier.ResourceChangeDecided(ctx, u.changeNotice(decided, label, decided.RequestedBy), approve, by); nErr != nil {
			log.Warn().Err(nErr).Msg("Failed to notify about the change decision")
		}
	}
	return u.changeView(ctx, calModel.NamedChangeRequest{ChangeRequest: decided, EventName: label.EventName, EventTag: label.EventTag}), nil
}

// applyChange puts an approved request into the reservation: the estimate, then the size, then the window. The
// tail gap still applies on top of a later end.
func (u *ResourceCalendarUseCase) applyChange(r *calModel.Reservation, c calModel.ChangeRequest, now time.Time) error {
	if c.Dynamic != nil {
		if err := r.Recalculate(r.Teams, r.PerTeam, r.LargestDevice, r.BufferPercent, *c.Dynamic, now); err != nil {
			return err
		}
	}
	if c.Size != nil {
		if err := r.SetSize(*c.Size, now); err != nil {
			return err
		}
	}
	if c.WindowStart != nil || c.WindowEnd != nil {
		start, end := r.Window.Start, r.Window.End
		if c.WindowStart != nil {
			start = *c.WindowStart
		}
		if c.WindowEnd != nil {
			tail := max(r.TailGap, u.cfg.TailGap)
			end = c.WindowEnd.Add(tail)
		}
		w, err := calModel.NewWindow(start, end)
		if err != nil {
			return err
		}
		if err = r.Reschedule(w, now); err != nil {
			return err
		}
	}
	return nil
}
