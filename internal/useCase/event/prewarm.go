package event

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

const (
	// prewarmImagesTTL is how long the image list of an event is reused: the pinned versions of an
	// event change rarely, and the engine pass runs every few seconds.
	prewarmImagesTTL = 5 * time.Minute
	// prewarmBackoff is the pause after a failed prewarm call (for example the image cache is off).
	prewarmBackoff = 5 * time.Minute
)

// PrewarmSummary is the state of the platform image cache for the images of an event, as the agent
// last reported it. UpdatedAt is zero until the first answer.
type PrewarmSummary struct {
	Total, Done, Warming, Queued, Failed, Skipped int
	UpdatedAt                                     time.Time
}

// Settled is true when no image is still being fetched.
func (s PrewarmSummary) Settled() bool { return s.Warming == 0 && s.Queued == 0 }

type prewarmState struct {
	mu       sync.Mutex
	images   map[uuid.UUID]cachedImages
	summary  map[uuid.UUID]PrewarmSummary
	failures map[uuid.UUID]string
	pauseTil time.Time
}

type cachedImages struct {
	images []string
	at     time.Time
}

func newPrewarmState() *prewarmState {
	return &prewarmState{images: map[uuid.UUID]cachedImages{}, summary: map[uuid.UUID]PrewarmSummary{}, failures: map[uuid.UUID]string{}}
}

func (s *prewarmState) summaryOf(eventID uuid.UUID) *PrewarmSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.summary[eventID]; ok {
		return &value
	}
	return nil
}

// prewarmEventImages fills the platform image cache with the images of the events whose stands deploy
// soon, so the burst of Labs at the deploy time pulls from the cache and not from the upstream
// registries. An event is prewarmed from `prewarmLead` before its deploy time until its start. The
// call is idempotent and asynchronous on the agent's side: every pass repeats it, which polls the
// state. It never fails the pass: a stand deploys fine without a warm cache.
func (u *EventUseCase) prewarmEventImages(ctx context.Context, now time.Time) {
	if u.prewarmLead <= 0 || !u.laboratoriesUsable(ctx) {
		return
	}
	prewarmer, ok := u.infra.(infraModel.ImagePrewarmer)
	if !ok {
		return
	}
	u.prewarm.mu.Lock()
	paused := now.Before(u.prewarm.pauseTil)
	u.prewarm.mu.Unlock()
	if paused {
		return
	}
	// The stand window opens at start minus the deploy lead; shifting the clock by the prewarm lead
	// selects the events whose prewarm window is open.
	eventIDs, err := u.stands.ListEvents(ctx, now.Add(u.prewarmLead))
	if err != nil {
		log.Error().Err(err).Msg("Image prewarm: failed to list events")
		return
	}
	for _, eventID := range eventIDs {
		if err = u.prewarmOneEvent(ctx, prewarmer, eventID, now); err != nil {
			log.Warn().Err(err).Str("event_id", eventID.String()).Msg("Image prewarm failed")
			u.prewarm.mu.Lock()
			u.prewarm.pauseTil = now.Add(prewarmBackoff)
			u.prewarm.mu.Unlock()
			return
		}
	}
}

func (u *EventUseCase) prewarmOneEvent(ctx context.Context, prewarmer infraModel.ImagePrewarmer, eventID uuid.UUID, now time.Time) error {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if !e.InfrastructureAllowed || !e.Lifecycle.Configured || !now.Before(e.Lifecycle.StartAt) {
		return nil
	}
	images, err := u.eventImages(ctx, eventID, now)
	if err != nil || len(images) == 0 {
		return err
	}
	states, err := prewarmer.PrewarmImages(ctx, images)
	if err != nil {
		return err
	}
	summary := PrewarmSummary{Total: len(states), UpdatedAt: now}
	var failed []string
	for _, state := range states {
		switch state.State {
		case infraModel.PrewarmDone:
			summary.Done++
		case infraModel.PrewarmWarming:
			summary.Warming++
		case infraModel.PrewarmQueued:
			summary.Queued++
		case infraModel.PrewarmSkipped:
			summary.Skipped++
		default:
			summary.Failed++
			failed = append(failed, state.Image+": "+state.Error)
		}
	}
	sort.Strings(failed)
	key := ""
	for _, f := range failed {
		key += f + "\n"
	}
	u.prewarm.mu.Lock()
	u.prewarm.summary[eventID] = summary
	changed := u.prewarm.failures[eventID] != key
	u.prewarm.failures[eventID] = key
	u.prewarm.mu.Unlock()
	if changed && key != "" {
		log.Warn().Str("event_id", eventID.String()).Strs("images", failed).Msg("Image prewarm: some images failed, labs will pull them from the upstream registry")
	}
	return nil
}

// eventImages lists the container images of every variant of every active infrastructure exercise
// of the event, sorted and without duplicates.
func (u *EventUseCase) eventImages(ctx context.Context, eventID uuid.UUID, now time.Time) ([]string, error) {
	u.prewarm.mu.Lock()
	cached, ok := u.prewarm.images[eventID]
	u.prewarm.mu.Unlock()
	if ok && now.Sub(cached.at) < prewarmImagesTTL {
		return cached.images, nil
	}
	resolver, ok := u.topologies.(VersionTopologyResolver)
	if !ok {
		return nil, nil
	}
	attachments, err := u.eventExercises.List(ctx, eventID)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
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
			return nil, resolveErr
		}
		for _, topology := range topologies {
			for _, device := range topology.Devices {
				if device.Image != "" {
					set[device.Image] = struct{}{}
				}
			}
		}
	}
	images := make([]string, 0, len(set))
	for image := range set {
		images = append(images, image)
	}
	sort.Strings(images)
	u.prewarm.mu.Lock()
	u.prewarm.images[eventID] = cachedImages{images: images, at: now}
	u.prewarm.mu.Unlock()
	return images, nil
}
