package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// reservationEnder is the optional part of the resource gate that tells when the event's reservation window ends.
type reservationEnder interface {
	EventReservationEnd(ctx context.Context, eventID uuid.UUID) (endsAt time.Time, found bool, err error)
}

// requireWithdrawAfterReservation refuses a withdrawal earlier than the end of the event's reservation: the end of
// the reservation window is the only point where the labs of the event are deleted, so the reservation is
// shortened first. An event without a reservation, or a gate that cannot say, has nothing to protect.
func (u *EventUseCase) requireWithdrawAfterReservation(ctx context.Context, eventID uuid.UUID, withdrawAt *time.Time) error {
	if withdrawAt == nil {
		return nil
	}
	ender, ok := u.resourceGate.(reservationEnder)
	if !ok {
		return nil
	}
	end, found, err := ender.EventReservationEnd(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the event reservation").Err()
	}
	if found && end.After(*withdrawAt) {
		return calModel.ErrWithdrawBeforeReservationEnd.
			WithPublicContext("reservationEnd", end.UTC().Format(time.RFC3339)).
			WithPublicContext("withdrawAt", withdrawAt.UTC().Format(time.RFC3339)).Err()
	}
	return nil
}

// labsEndWithReservation: the labs and groups of the event are deleted at the end of its reservation window and at
// no other time; neither the withdrawal nor the teardown delay of the event deletes anything (owner, 2026-10-31).
// Only a deployment without a resource calendar (the gate cannot tell about reservations) keeps the older
// teardown at withdrawal, so that its labs still have a point of deletion.
func (u *EventUseCase) labsEndWithReservation() bool {
	_, ok := u.resourceGate.(reservationHolder)
	return ok
}
