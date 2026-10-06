package resourceCalendar

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// next free detail code: 15

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
)
