package event

import (
	"context"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// standSchedule is what the stand engine derives for one event at one moment: when its first labs are due, which
// sets' labs are not due yet (a later stage), and when the next ones become due.
type standSchedule struct {
	// InitialDeployAt is when the labs of the first moment (sets without a stage and the first stage) start
	// deploying: the event start minus the computed lead.
	InitialDeployAt time.Time
	// NotDue are the sets whose labs wait for their deploy lead.
	NotDue []uuid.UUID
	// NextDueAt is the earliest moment a set in NotDue becomes due; nil when none waits.
	NextDueAt *time.Time
}

// schedulerCapacity is the optional capability of the infrastructure port that knows how many pods the agents
// that are used start at once (0: no limit or unknown).
type schedulerCapacity interface {
	SchedulerMaxPods() int
}

// leadPlanTTL is how long the workload of an event is reused between engine passes.
const leadPlanTTL = time.Minute

type cachedLeadPlan struct {
	sets  []eventModel.SetLoad
	extra int
	at    time.Time
}

// maxPods is the pods the serving agents start at once, 0 when unknown.
func (u *EventUseCase) maxPods() int {
	if capacity, ok := u.infra.(schedulerCapacity); ok {
		return capacity.SchedulerMaxPods()
	}
	return 0
}

// prePull is the image pre-pull share of a lead: only when the platform image cache can warm the images.
func (u *EventUseCase) prePull() time.Duration {
	if u.prewarmLead <= 0 {
		return 0
	}
	if _, ok := u.infra.(infraModel.ImagePrewarmer); !ok {
		return 0
	}
	return eventStandModel.DefaultImagePrePull
}

// leadFunc is the computed pre-deploy lead as a function of the pods to start.
func (u *EventUseCase) leadFunc() func(pods int) time.Duration {
	maxPods, prePull := u.maxPods(), u.prePull()
	return func(pods int) time.Duration {
		return eventStandModel.ComputeLead(eventStandModel.LeadInput{Workload: pods, MaxPods: maxPods, PrePull: prePull})
	}
}

// setLoads reads the pods each active set puts on the cluster for all teams (teams x devices of its largest
// variant) and the group pods that exist from the event start (a VPN and a gateway per team).
func (u *EventUseCase) setLoads(ctx context.Context, e eventModel.Event, now time.Time) ([]eventModel.SetLoad, int, error) {
	u.placement.mu.Lock()
	cached, ok := u.placement.plans[e.ID]
	u.placement.mu.Unlock()
	if ok && now.Sub(cached.at) < leadPlanTTL {
		return cached.sets, cached.extra, nil
	}
	plan, err := u.resourcePlanInputs(ctx, e.ID)
	if err != nil {
		return nil, 0, err
	}
	links, err := u.eventExercises.List(ctx, e.ID)
	if err != nil {
		return nil, 0, err
	}
	stageOf := make(map[uuid.UUID]*uuid.UUID, len(links))
	for _, link := range activeAttachments(links) {
		stageOf[link.ID] = link.StageID
	}
	sets := make([]eventModel.SetLoad, 0, len(plan.tasks))
	for _, task := range plan.tasks {
		stage, active := stageOf[task.EventExerciseID]
		if !active {
			continue
		}
		sets = append(sets, eventModel.SetLoad{ExerciseID: task.EventExerciseID, StageID: stage, Pods: plan.teams * task.labDevices})
	}
	extra := 0
	if e.InfrastructureAllowed {
		extra = plan.teams * 2
	}
	u.placement.mu.Lock()
	u.placement.plans[e.ID] = cachedLeadPlan{sets: sets, extra: extra, at: now}
	u.placement.mu.Unlock()
	return sets, extra, nil
}

// standSchedule computes the deploy schedule of an event. A failure to read the workload falls back to the
// shortest lead with nothing held back, so a read error never stops a stand from deploying.
func (u *EventUseCase) standSchedule(ctx context.Context, e eventModel.Event, now time.Time) standSchedule {
	lead := u.leadFunc()
	stages, err := u.stages.List(ctx, e.ID)
	if err != nil {
		log.Warn().Err(err).Str("event_id", e.ID.String()).Msg("Stand schedule: cannot read the stages, deploying everything")
		return standSchedule{InitialDeployAt: e.Lifecycle.StartAt.Add(-lead(0))}
	}
	sets, extra, err := u.setLoads(ctx, e, now)
	if err != nil {
		log.Warn().Err(err).Str("event_id", e.ID.String()).Msg("Stand schedule: cannot read the workload, using the shortest lead")
		return standSchedule{InitialDeployAt: e.Lifecycle.StartAt.Add(-lead(0))}
	}
	plan := eventModel.PlanStageDeploy(e.Lifecycle.StartAt, stages, sets, extra, lead)
	schedule := standSchedule{InitialDeployAt: e.Lifecycle.StartAt.Add(-plan.InitialLead), NotDue: plan.NotDue(now, stages, sets)}
	if len(schedule.NotDue) > 0 {
		due := make([]time.Time, 0, len(schedule.NotDue))
		for _, id := range schedule.NotDue {
			due = append(due, plan.DueAt[id])
		}
		sort.Slice(due, func(i, j int) bool { return due[i].Before(due[j]) })
		schedule.NextDueAt = &due[0]
	}
	return schedule
}
