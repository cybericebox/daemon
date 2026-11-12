package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
)

// reservationEnder is the optional part of the resource gate that tells when the event's reservation window ends.
type reservationEnder interface {
	EventReservationEnd(ctx context.Context, eventID uuid.UUID) (endsAt time.Time, found bool, err error)
}

// reservationShortener is the optional part of the resource gate that ends the event's reservation window earlier.
type reservationShortener interface {
	ShortenEventReservation(ctx context.Context, eventID uuid.UUID, newEnd time.Time, by uuid.UUID) (oldEnd time.Time, err error)
}

// shortenReservationToWithdrawal ends the event's reservation window at the new withdrawal when it ends later (owner,
// 2026-10-31): saving the schedule is one action, with no second request. The labs under the reservation are stopped
// and deleted at the new end like at any end. It reports the new end and how many labs run now (zero and nil when the
// window was not touched), for the answer to show the result.
func (u *EventUseCase) shortenReservationToWithdrawal(ctx context.Context, eventID uuid.UUID, withdrawAt *time.Time, by uuid.UUID) (*time.Time, int32, error) {
	shortener, ok := u.resourceGate.(reservationShortener)
	if !ok || withdrawAt == nil {
		return nil, 0, nil
	}
	running := int32(0)
	if u.repo == nil {
		// no store (a unit test): nothing to count
	} else if labs, err := eventLabAllocationRepo.New(u.repo).Labs(ctx, eventID); err == nil {
		for _, l := range labs {
			if l.DesiredState == "Running" {
				running++
			}
		}
	}
	oldEnd, err := shortener.ShortenEventReservation(ctx, eventID, *withdrawAt, by)
	if err != nil {
		return nil, 0, err
	}
	if oldEnd.IsZero() {
		return nil, 0, nil
	}
	end := *withdrawAt
	return &end, running, nil
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
