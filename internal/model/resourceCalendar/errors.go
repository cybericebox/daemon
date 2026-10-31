package resourceCalendar

import (
	"net/http"

	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// next free detail code: 23 (20 was retired)

var (
	// ErrWindowInvalid: the time range of a reservation or a booking is empty, reversed or out of bounds.
	ErrWindowInvalid = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The reservation window is invalid").
				WithDetailCode(1)

	// ErrReservationInvalid: the size, the team count or the buffer of a reservation is invalid.
	ErrReservationInvalid = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The reservation is invalid").
				WithDetailCode(2)

	// ErrReservationNotFound: no such reservation.
	ErrReservationNotFound = err.ErrObjectNotFound.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("Reservation not found").
				WithDetailCode(3)

	// ErrReservationConflict: the reservation does not fit the agents by packing in some slots. The
	// admin repeats the request with allow_conflicts to keep it and resolve the conflict by hand; the
	// context carries the first conflicting slot.
	ErrReservationConflict = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The reservation does not fit the available resources").
				WithDetailCode(4)

	// ErrChangeRequestNotFound: no such change request.
	ErrChangeRequestNotFound = err.ErrObjectNotFound.WithObjectCode(model.ResourceCalendarObjectCode).
					WithMessage("Change request not found").
					WithDetailCode(5)

	// ErrChangeRequestDecided: the change request was decided already.
	ErrChangeRequestDecided = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The change request was decided already").
				WithDetailCode(6)

	// ErrChangeRequestInvalid: a change request must change the size, the window or the dynamic estimate,
	// and carry a reason.
	ErrChangeRequestInvalid = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The change request is invalid").
				WithDetailCode(7)

	// ErrNotEnoughReserved refuses a task for a running event whose reservation cannot hold it for all
	// teams. The organizer requests an extension.
	ErrNotEnoughReserved = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("Not enough reserved resources, request an extension").
				WithDetailCode(8)

	// ErrNoTestLabRoom: no free resources for a test laboratory now; the context carries the nearest
	// free window ("nearest_from") the author may book.
	ErrNoTestLabRoom = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("No free resources now, the nearest window is shown").
				WithDetailCode(9)

	// ErrBookingNotFound: no such test lab booking.
	ErrBookingNotFound = err.ErrObjectNotFound.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("Booking not found").
				WithDetailCode(10)

	// ErrAlarmNotFound: no such readiness alarm.
	ErrAlarmNotFound = err.ErrObjectNotFound.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("Readiness alarm not found").
				WithDetailCode(11)

	// ErrBookingInvalid: the booking is too short, too long or too far ahead.
	ErrBookingInvalid = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The booking is invalid").
				WithDetailCode(12)

	// ErrNoReservation: the event has no resource reservation.
	ErrNoReservation = err.ErrObjectNotFound.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The event has no resource reservation").
				WithDetailCode(13)

	// ErrChangeRequestPending: the event has a change request that waits for a decision; a new one is sent after it.
	ErrChangeRequestPending = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The event has a change request that waits for a decision").
				WithDetailCode(14)

	// ErrUnitNotPlaced: every share of the reservation is taken, so a new team (or preview owner) has no agent.
	ErrUnitNotPlaced = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("The reservation has no free place for this team").
				WithDetailCode(15)

	// ErrTestLengthNotAllowed: the run length a test laboratory asks for is not one of the offered ones; the
	// public context "allowed" lists them in minutes. (Kept in the calendar block: the exercise object has no free
	// detail code.)
	ErrTestLengthNotAllowed = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
				WithMessage("This run length is not offered").
				WithDetailCode(16)

	// ErrTestLabReservationFull: the reservation a test laboratory runs under (the author's booking, the run) has
	// no room left for it.
	ErrTestLabReservationFull = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
					WithMessage("The reservation has no room for this laboratory").
					WithDetailCode(17)

	// ErrClusterReservationNotReady: the cluster reservation of a test laboratory is not on its agent yet; the
	// author tries again in a minute.
	ErrClusterReservationNotReady = err.ErrConflict.WithObjectCode(model.ResourceCalendarObjectCode).
					WithMessage("The resources are not ready yet, try again in a minute").
					WithDetailCode(18).
					WithHTTPCode(http.StatusServiceUnavailable)

	// ErrReservationEndsAfterWithdraw: the end of an event's reservation window may not be later than the withdrawal
	// of the event (the end of the window is the only point where the cluster deletes the labs). The public context
	// has bound (the withdrawal) and requested (the end), RFC 3339; reservationEnd and withdrawAt repeat them.
	ErrReservationEndsAfterWithdraw = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
					WithMessage("The reservation must end before the event is withdrawn").
					WithDetailCode(19)

	// ErrReservationStartTooLate: the window of an event's reservation starts later than the lead before the event
	// start allows (the capacity must be connected that long before). The public context has bound (the latest
	// start) and requested (the start), RFC 3339.
	ErrReservationStartTooLate = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
					WithMessage("The reservation must start earlier, before the lead of the event start").
					WithDetailCode(21)

	// ErrReservationEndsBeforeEventEnd: the window of an event's reservation ends before the event does. The public
	// context has bound (the event end) and requested (the end), RFC 3339.
	ErrReservationEndsBeforeEventEnd = err.ErrInvalidData.WithObjectCode(model.ResourceCalendarObjectCode).
						WithMessage("The reservation must not end before the event ends").
						WithDetailCode(22)
)
