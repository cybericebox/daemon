package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// The end of an event's reservation window is the only point where the labs and groups of the event are deleted, so
// it may not be later than the withdrawal of the event (the owner's decision of 2026-10-31).

// withdrawAt is the scheduled withdrawal of a configured event; nil when there is none.
func withdrawAt(e eventModel.Event) *time.Time {
	if !e.Lifecycle.Configured {
		return nil
	}
	return e.Lifecycle.WithdrawAt
}

// requireEndBeforeWithdraw refuses a window that ends after the event is withdrawn.
func requireEndBeforeWithdraw(e eventModel.Event, end time.Time) error {
	if withdraw := withdrawAt(e); withdraw != nil && end.After(*withdraw) {
		return calModel.ErrReservationEndsAfterWithdraw.
			WithPublicContext("reservationEnd", end.UTC().Format(time.RFC3339)).
			WithPublicContext("withdrawAt", withdraw.UTC().Format(time.RFC3339)).Err()
	}
	return nil
}

// requireReservationEndBeforeWithdraw is the same check for a change of a reservation of an event, found by id.
func (u *ResourceCalendarUseCase) requireReservationEndBeforeWithdraw(ctx context.Context, eventID uuid.UUID, end time.Time) error {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return platformErr(err, "Failed to get the event for its reservation")
	}
	return requireEndBeforeWithdraw(e, end)
}

// EventReservationEnd tells when the window of the event's active reservation ends; found is false when the event
// has none. The event lifecycle uses it so an event is not withdrawn before its reservation ends.
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
