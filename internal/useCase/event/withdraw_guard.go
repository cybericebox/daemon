package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

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

// labsEndWithReservation says whether the labs of the event are deleted by the end of its reservation window and by
// nothing else (owner, 2026-10-31): the event has an active reservation that ends no later than its withdrawal.
// Anything else (no reservation, a cancelled one, one that outlives the withdrawal, a deployment without a calendar)
// keeps the withdrawal teardown, so the labs, the VPN keys and the access always have a point of deletion. A reservation
// that cannot be read counts as held for this pass: nothing is deleted on a guess, the next pass asks again.
func (u *EventUseCase) labsEndWithReservation(ctx context.Context, eventID uuid.UUID, withdrawAt *time.Time) bool {
	holder, ok := u.resourceGate.(reservationHolder)
	if !ok {
		return false
	}
	reserved, err := holder.HasEventReservation(ctx, eventID)
	if err != nil {
		log.Warn().Err(err).Str("event_id", eventID.String()).Msg("Withdrawal teardown: cannot read the reservation, the pass is skipped")
		return true
	}
	if !reserved {
		return false
	}
	if ender, ok := u.resourceGate.(reservationEnder); ok && withdrawAt != nil {
		end, found, endErr := ender.EventReservationEnd(ctx, eventID)
		if endErr != nil {
			log.Warn().Err(endErr).Str("event_id", eventID.String()).Msg("Withdrawal teardown: cannot read the reservation end, the pass is skipped")
			return true
		}
		if found && end.After(*withdrawAt) {
			return false
		}
	}
	return true
}
