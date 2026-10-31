package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// The window of an event's reservation (owner, 2026-10-31):
//   - it starts at least the lead before the event start (earlier is fine, later is not);
//   - it ends no earlier than the event ends (the tail after it is only the default) and no later than the
//     withdrawal of the event: the end of the window is the only point where the labs are deleted.

// minLead is the least time before the event start the window may start.
func (u *ResourceCalendarUseCase) minLead() time.Duration {
	return eventStandModel.PlanningLead + u.cfg.LeadMargin
}

// withdrawAt is the scheduled withdrawal of a configured event; nil when there is none.
func withdrawAt(e eventModel.Event) *time.Time {
	if !e.Lifecycle.Configured {
		return nil
	}
	return e.Lifecycle.WithdrawAt
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// requireWindowBounds refuses a window that starts too late, ends before the event or ends after its withdrawal.
func (u *ResourceCalendarUseCase) requireWindowBounds(e eventModel.Event, w calModel.Window) error {
	if !e.Lifecycle.Configured {
		return nil
	}
	if latest := e.Lifecycle.StartAt.Add(-u.minLead()); w.Start.After(latest) {
		return calModel.ErrReservationStartTooLate.
			WithPublicContext("bound", rfc3339(latest)).WithPublicContext("requested", rfc3339(w.Start)).Err()
	}
	if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && w.End.Before(*finish) {
		return calModel.ErrReservationEndsBeforeEventEnd.
			WithPublicContext("bound", rfc3339(*finish)).WithPublicContext("requested", rfc3339(w.End)).Err()
	}
	if withdraw := withdrawAt(e); withdraw != nil && w.End.After(*withdraw) {
		return calModel.ErrReservationEndsAfterWithdraw.
			WithPublicContext("bound", rfc3339(*withdraw)).WithPublicContext("requested", rfc3339(w.End)).
			WithPublicContext("reservationEnd", rfc3339(w.End)).WithPublicContext("withdrawAt", rfc3339(*withdraw)).Err()
	}
	return nil
}

// requireReservationBounds is the same check for the window of a reservation of an event, found by id.
func (u *ResourceCalendarUseCase) requireReservationBounds(ctx context.Context, eventID uuid.UUID, w calModel.Window) error {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return platformErr(err, "Failed to get the event for its reservation")
	}
	return u.requireWindowBounds(e, w)
}

// EventReservationEnd tells when the window of the event's active reservation ends; found is false when the event
// has none.
func (u *ResourceCalendarUseCase) EventReservationEnd(ctx context.Context, eventID uuid.UUID) (time.Time, bool, error) {
	r, err := u.store.GetEventReservation(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, platformErr(err, "Failed to read the event reservation")
	}
	return r.Window.End, true, nil
}

// ShortenEventReservation ends the event's reservation window at newEnd when it ends later (the event is withdrawn
// earlier than the window ends): the labs under it are stopped and deleted at the new end. It reports the old end,
// or the zero time when nothing changed (no reservation, or it already ends by newEnd). The sizes are not touched.
func (u *ResourceCalendarUseCase) ShortenEventReservation(ctx context.Context, eventID uuid.UUID, newEnd time.Time, by uuid.UUID) (oldEnd time.Time, err error) {
	current, found, err := u.EventReservationEnd(ctx, eventID)
	if err != nil || !found || !current.After(newEnd) {
		return time.Time{}, err
	}
	if _, err = u.SetEventResourceReservation(ctx, eventID, EventReservationInput{WindowEnd: &newEnd, ForceShrink: true}, by); err != nil {
		return time.Time{}, err
	}
	return current, nil
}
