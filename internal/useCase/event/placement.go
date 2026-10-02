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
	mu   sync.Mutex
	need map[uuid.UUID]cachedNeed
}

type cachedNeed struct {
	need infraModel.PlacementNeed
	at   time.Time
}

// withPlacementNeed tells the placement of a team's group which labs the event will put on it: a team
// lives on one agent, so an agent that cannot run one of the event's tasks within its limits is not a
// candidate. A failure to read the topologies only loses that filter; the deploy of each lab still
// checks its own.
func (u *EventUseCase) withPlacementNeed(ctx context.Context, eventID uuid.UUID) context.Context {
	need, err := u.eventPlacementNeed(ctx, eventID)
	if err != nil {
		log.Warn().Err(err).Str("event_id", eventID.String()).Msg("Placement: cannot read the labs of the event, placing without the limits filter")
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
	resolver, ok := u.topologies.(VersionTopologyResolver)
	if !ok {
		return infraModel.PlacementNeed{}, nil
	}
	attachments, err := u.eventExercises.List(ctx, eventID)
	if err != nil {
		return infraModel.PlacementNeed{}, err
	}
	var need infraModel.PlacementNeed
	versions := map[uuid.UUID]struct{}{}
	for _, attachment := range attachments {
		if attachment.Status != eventExerciseModel.StatusActive {
			continue
		}
		if _, seen := versions[attachment.ExerciseVersionID]; seen {
			continue
		}
		versions[attachment.ExerciseVersionID] = struct{}{}
		topologies, resolveErr := resolver.ResolveVersionTopologies(ctx, attachment.ExerciseVersionID)
		if resolveErr != nil {
			return infraModel.PlacementNeed{}, resolveErr
		}
		for _, topology := range topologies {
			need.Labs = append(need.Labs, infraModel.DemandOf(topology))
		}
	}
	u.placement.mu.Lock()
	u.placement.need[eventID] = cachedNeed{need: need, at: now}
	u.placement.mu.Unlock()
	return need, nil
}
