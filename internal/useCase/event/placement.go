package event

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// placementNeedTTL is how long the labs an event needs are reused; the pinned versions change rarely.
const placementNeedTTL = time.Minute

type placementCache struct {
	mu    sync.Mutex
	need  map[uuid.UUID]cachedNeed
	plans map[uuid.UUID]cachedLeadPlan
}

type cachedNeed struct {
	need infraModel.PlacementNeed
	at   time.Time
}

// withPlacementNeed tells the placement of a team's group what the event will put on it: a team lives on one
// agent, so an agent whose device maxima are below the event's largest device is not a candidate, and the
// group's own pods are sized by the event's maximum team size (VPN) and its internet labs (gateway). A
// failure to read the event only loses that: the deploy of each lab still checks its own.
func (u *EventUseCase) withPlacementNeed(ctx context.Context, eventID uuid.UUID) context.Context {
	// Only a port that knows the agents (the fleet) can place and size by it.
	if _, ok := u.infra.(resourcePlanner); !ok {
		return ctx
	}
	need, err := u.eventPlacementNeed(ctx, eventID)
	if err != nil {
		log.Warn().Err(err).Str("event_id", eventID.String()).Msg("Placement: cannot read the labs of the event, placing without its needs")
		return ctx
	}
	return infraModel.WithPlacementNeed(ctx, need)
}

func (u *EventUseCase) eventPlacementNeed(ctx context.Context, eventID uuid.UUID) (infraModel.PlacementNeed, error) {
	now := time.Now()
	u.placement.mu.Lock()
	cached, ok := u.placement.need[eventID]
	u.placement.mu.Unlock()
	if ok && now.Sub(cached.at) < placementNeedTTL {
		return cached.need, nil
	}
	plan, err := u.resourcePlanInputs(ctx, eventID)
	if err != nil {
		return infraModel.PlacementNeed{}, err
	}
	need := plan.placementNeed()
	u.placement.mu.Lock()
	u.placement.need[eventID] = cachedNeed{need: need, at: now}
	u.placement.mu.Unlock()
	return need, nil
}

// activeAttachments are the event's attachments that are in force.
func activeAttachments(all []eventExerciseModel.EventExercise) []eventExerciseModel.EventExercise {
	out := make([]eventExerciseModel.EventExercise, 0, len(all))
	for _, attachment := range all {
		if attachment.Status == eventExerciseModel.StatusActive && attachment.SupersededAt == nil {
			out = append(out, attachment)
		}
	}
	return out
}
