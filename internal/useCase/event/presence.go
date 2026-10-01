package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
)

// TouchParticipantPresence records that the user was online on the event now.
// The store ignores a touch within a minute of the stored time.
func (u *EventUseCase) TouchParticipantPresence(ctx context.Context, eventID, userID uuid.UUID) error {
	return u.participants.TouchPresence(ctx, eventID, userID, time.Now())
}
