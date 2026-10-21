package event

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// ResultsFreezeBanner reads the freeze state of the event for one viewer, for the freeze banner of the
// event site. It shares the event/config cache with results reads without loading any score
// projection. The view has Applied set only while the freeze applies to this viewer; where the viewer
// may not read the results at all (hidden event, participants-only results, not started) there is
// nothing to show, so that is an empty view, never an error.
func (u *EventUseCase) ResultsFreezeBanner(ctx context.Context, eventID uuid.UUID, access ResultsAccess) (ResultsFreezeView, error) {
	now := time.Now()
	shared, err := u.sharedResults(ctx, eventID)
	if err != nil {
		if errors.Is(err, eventModel.ErrEventNotFound.Err()) {
			return ResultsFreezeView{}, nil
		}
		return ResultsFreezeView{}, err
	}
	// No freeze right now: no reason to look the viewer up.
	if view := freezeView(shared.cfg.Results, shared.event.Lifecycle.EffectiveFinishAt(), now); !view.Active {
		return view, nil
	}
	policy := shared.policy(u.loadResultsViewer(ctx, eventID, access))
	if policy.readErr(now, false) != nil {
		return ResultsFreezeView{}, nil //nolint:nilerr // no results access means no freeze banner, not an error
	}
	return policy.freeze(now, false), nil
}
