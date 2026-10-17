package event

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

const (
	// prewarmImagesTTL is how long the prewarm plan (images and credentials) of an event is reused: the pinned versions of an
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
	plans    map[uuid.UUID]cachedPlan
	summary  map[uuid.UUID]PrewarmSummary
	failures map[uuid.UUID]string
	pauseTil time.Time
}

// cachedPlan holds pull credentials in memory only; they are never logged.
type cachedPlan struct {
	plan prewarmPlan
	at   time.Time
}

// prewarmPlan is what the agent is asked to pull for an event: batches of images that share one
// credential set (exactly what their labs are deployed with), and the images that cannot be pulled
// at all, which are skipped.
type prewarmPlan struct {
	batches []prewarmBatch
	skipped []imageRegistryModel.Availability
}

type prewarmBatch struct {
	images []string
	creds  []infraModel.RegistryCredential
}

func newPrewarmState() *prewarmState {
	return &prewarmState{plans: map[uuid.UUID]cachedPlan{}, summary: map[uuid.UUID]PrewarmSummary{}, failures: map[uuid.UUID]string{}}
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
	// The stand window opens at most MaxLead before the start; shifting the clock by the prewarm lead selects the
	// events that may be in their prewarm window, which prewarmOneEvent narrows by the computed lead.
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
	if !e.InfrastructureAllowed || !e.Lifecycle.Configured {
		return nil
	}
	// Before the event: from prewarmLead ahead of the first deploy until the start. After it: the images of the
	// next stage are pulled from prewarmLead ahead of that stage's deploy (the pre-pull trigger).
	schedule := u.standSchedule(ctx, e, now)
	if now.Before(e.Lifecycle.StartAt) {
		if now.Before(schedule.InitialDeployAt.Add(-u.prewarmLead)) {
			return nil
		}
	} else if schedule.NextDueAt == nil || now.Before(schedule.NextDueAt.Add(-u.prewarmLead)) {
		return nil
	}
	plan, err := u.eventPrewarmPlan(ctx, eventID, now)
	if err != nil || (len(plan.batches) == 0 && len(plan.skipped) == 0) {
		return err
	}
	summary := PrewarmSummary{UpdatedAt: now}
	var failed, unavailable []string
	for _, batch := range plan.batches {
		states, err := prewarmer.PrewarmImages(ctx, batch.images, batch.creds)
		if err != nil {
			return err
		}
		summary.Total += len(states)
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
	}
	// Images that cannot be pulled are not sent: their labs are refused at deploy («Образ недоступний»).
	for _, a := range plan.skipped {
		summary.Total++
		summary.Skipped++
		unavailable = append(unavailable, a.Image+": "+string(a.Reason))
	}
	sort.Strings(failed)
	sort.Strings(unavailable)
	key := ""
	for _, f := range failed {
		key += f + "\n"
	}
	for _, f := range unavailable {
		key += "unavailable " + f + "\n"
	}
	u.prewarm.mu.Lock()
	u.prewarm.summary[eventID] = summary
	changed := u.prewarm.failures[eventID] != key
	u.prewarm.failures[eventID] = key
	u.prewarm.mu.Unlock()
	if changed && len(failed) > 0 {
		log.Warn().Str("event_id", eventID.String()).Strs("images", failed).Msg("Image prewarm: some images failed, labs will pull them from the upstream registry")
	}
	if changed && len(unavailable) > 0 {
		log.Warn().Str("event_id", eventID.String()).Strs("images", unavailable).Msg("Image prewarm: some images are unavailable and skipped, their labs will not start")
	}
	return nil
}

// eventPrewarmPlan checks the container images of every variant of every active infrastructure exercise
// of the event with the event's scope, the same check as a stand deploy: each variant's images are prepared
// together, so the credentials sent are exactly those its labs get, and an unavailable image is skipped
// (the rest of its variant is still prewarmed). Batches share one credential set; images are sorted and
// without duplicates. The plan is reused for prewarmImagesTTL.
func (u *EventUseCase) eventPrewarmPlan(ctx context.Context, eventID uuid.UUID, now time.Time) (prewarmPlan, error) {
	u.prewarm.mu.Lock()
	cached, ok := u.prewarm.plans[eventID]
	u.prewarm.mu.Unlock()
	if ok && now.Sub(cached.at) < prewarmImagesTTL {
		return cached.plan, nil
	}
	resolver, ok := u.topologies.(VersionTopologyResolver)
	if !ok {
		return prewarmPlan{}, nil
	}
	attachments, err := u.eventExercises.List(ctx, eventID)
	if err != nil {
		return prewarmPlan{}, err
	}
	b := newPrewarmPlanBuilder()
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
			return prewarmPlan{}, resolveErr
		}
		for _, topology := range topologies {
			if err = u.planTopologyPrewarm(ctx, eventID, topology, b); err != nil {
				return prewarmPlan{}, err
			}
		}
	}
	plan := b.plan()
	u.prewarm.mu.Lock()
	u.prewarm.plans[eventID] = cachedPlan{plan: plan, at: now}
	u.prewarm.mu.Unlock()
	return plan, nil
}

// planTopologyPrewarm prepares one variant's images; when some are unavailable they are skipped and
// the remaining devices are prepared once more on their own.
func (u *EventUseCase) planTopologyPrewarm(ctx context.Context, eventID uuid.UUID, topology exerciseModel.Topology, b *prewarmPlanBuilder) error {
	topology.Devices = slices.DeleteFunc(slices.Clone(topology.Devices), func(d exerciseModel.Device) bool { return d.Image == "" })
	for attempt := 0; attempt < 2 && len(topology.Devices) > 0; attempt++ {
		prepared, err := u.prepareEventLabImages(ctx, eventID, topology)
		if err == nil {
			b.add(topology, prepared.Topology, prepared.Credentials)
			return nil
		}
		if !errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()) {
			return err
		}
		bad := map[string]bool{}
		for _, a := range prepared.Unavailable {
			bad[a.Image] = true
			b.skip(a)
		}
		topology.Devices = slices.DeleteFunc(topology.Devices, func(d exerciseModel.Device) bool { return bad[d.Image] })
	}
	return nil
}

type prewarmPlanBuilder struct {
	batches  map[string]*prewarmBatch
	assigned map[string]bool // images (as sent) already in a batch
	included map[string]bool // stored references that are prewarmed
	skipped  map[string]imageRegistryModel.Availability
}

func newPrewarmPlanBuilder() *prewarmPlanBuilder {
	return &prewarmPlanBuilder{batches: map[string]*prewarmBatch{}, assigned: map[string]bool{}, included: map[string]bool{}, skipped: map[string]imageRegistryModel.Availability{}}
}

func (b *prewarmPlanBuilder) skip(a imageRegistryModel.Availability) {
	if _, ok := b.skipped[a.Image]; !ok {
		b.skipped[a.Image] = a
	}
}

// add puts the prepared images (stored is the input, prepared the same devices rewritten) into the
// batch of their credential set.
func (b *prewarmPlanBuilder) add(stored, prepared exerciseModel.Topology, creds []infraModel.RegistryCredential) {
	key := credentialSetKey(creds)
	batch := b.batches[key]
	if batch == nil {
		batch = &prewarmBatch{creds: creds}
		b.batches[key] = batch
	}
	for i, d := range prepared.Devices {
		if i < len(stored.Devices) {
			b.included[stored.Devices[i].Image] = true
		}
		if d.Image == "" || b.assigned[d.Image] {
			continue
		}
		b.assigned[d.Image] = true
		batch.images = append(batch.images, d.Image)
	}
}

func (b *prewarmPlanBuilder) plan() prewarmPlan {
	var out prewarmPlan
	for _, batch := range b.batches {
		if len(batch.images) > 0 {
			sort.Strings(batch.images)
			out.batches = append(out.batches, *batch)
		}
	}
	sort.Slice(out.batches, func(i, j int) bool { return out.batches[i].images[0] < out.batches[j].images[0] })
	for image, a := range b.skipped {
		if !b.included[image] {
			out.skipped = append(out.skipped, a)
		}
	}
	sort.Slice(out.skipped, func(i, j int) bool { return out.skipped[i].Image < out.skipped[j].Image })
	return out
}

// credentialSetKey identifies a credential set in memory (it holds the passwords and is never logged).
func credentialSetKey(creds []infraModel.RegistryCredential) string {
	parts := make([]string, 0, len(creds))
	for _, c := range creds {
		parts = append(parts, c.Registry+"\x00"+c.Username+"\x00"+c.Password)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}
