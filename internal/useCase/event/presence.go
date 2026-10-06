package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
)

// TouchParticipantPresence records that the user was online on the event now.
// The store ignores a touch within a minute of the stored time.
func (u *EventUseCase) TouchParticipantPresence(ctx context.Context, eventID, userID uuid.UUID) error {
	// Only a participant of the event leaves a trace on it: the route runs before anything checks
	// that the caller belongs to the event, and the event id comes from the URL.
	_, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil
		}
		return err
	}
	// Any row (applied, invited, approved) is a participant.
	return u.participants.TouchPresence(ctx, eventID, userID, time.Now())
}
