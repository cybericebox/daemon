package event

import (
	"context"

	"github.com/gofrs/uuid"
)

// ForceReservationWait makes the use case read the reservation as missing (true) or present (false).
func (u *EventUseCase) ForceReservationWait(wait bool) {
	u.reservationWait = func(context.Context, uuid.UUID) bool { return wait }
}
