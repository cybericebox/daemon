package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
)

// GetResultsFreeze reads only the authorized viewer's freeze state. It shares
// the event/config cache with results reads without loading any score projection.
func (u *EventUseCase) GetResultsFreeze(ctx context.Context, eventID uuid.UUID, access ResultsAccess) (ResultsFreezeView, error) {
	now := time.Now()
	shared, err := u.sharedResults(ctx, eventID)
	if err != nil {
		return ResultsFreezeView{}, err
	}
	policy := shared.policy(u.loadResultsViewer(ctx, eventID, access))
	if err := policy.readErr(now, false); err != nil {
		return ResultsFreezeView{}, err
	}
	return policy.freeze(now, false), nil
}
