package jobsRegistry

import (
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/cybericebox/daemon/internal/jobs/accountinactivity"
	"github.com/cybericebox/daemon/internal/jobs/agentmaintenance"
	"github.com/cybericebox/daemon/internal/jobs/broadcastsend"
	"github.com/cybericebox/daemon/internal/jobs/dataretention"
	"github.com/cybericebox/daemon/internal/jobs/errorjournal"
	"github.com/cybericebox/daemon/internal/jobs/eventanalytics"
	"github.com/cybericebox/daemon/internal/jobs/eventformdelivery"
	"github.com/cybericebox/daemon/internal/jobs/eventmail"
	"github.com/cybericebox/daemon/internal/jobs/eventscoringpopulation"
	"github.com/cybericebox/daemon/internal/jobs/eventstands"
	"github.com/cybericebox/daemon/internal/jobs/idempotencygc"
	"github.com/cybericebox/daemon/internal/jobs/labaccesssync"
	"github.com/cybericebox/daemon/internal/jobs/labcleanup"
	"github.com/cybericebox/daemon/internal/jobs/labgroupsweep"
	"github.com/cybericebox/daemon/internal/jobs/mediagc"
	"github.com/cybericebox/daemon/internal/jobs/notify"
	"github.com/cybericebox/daemon/internal/jobs/resourcecalendar"
	"github.com/cybericebox/daemon/internal/jobs/resultchangegc"
	"github.com/cybericebox/daemon/internal/jobs/signalprocessing"
	"github.com/cybericebox/daemon/internal/jobs/testdeploygc"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

// iUseCase is the worker layer's hidden, unified requirement on the application's
// use-case aggregate — the union of every job's narrow port. It is satisfied
// structurally by the concrete *iUseCase.UseCase handed to RegisterAll (the worker
// package never imports the iUseCase package). Add a job's port here when wiring it.
type (
	iUseCase interface {
		notifyJob.IUseCase
		broadcastsendJob.IUseCase
		mediagcJob.IUseCase
		idempotencygcJob.IUseCase
		eventscoringpopulationJob.IUseCase
		eventformdeliveryJob.IUseCase
		eventstandsJob.IUseCase
		agentmaintenanceJob.IUseCase
		eventmailJob.IUseCase
		resultchangegcJob.IUseCase
		testdeploygcJob.IUseCase
		labcleanupJob.IUseCase
		labgroupsweepJob.IUseCase
		labaccesssyncJob.IUseCase
		signalprocessingJob.IUseCase
		dataretentionJob.IUseCase
		accountinactivityJob.IUseCase
		eventanalyticsJob.IUseCase
		errorjournalJob.IUseCase
		resourcecalendarJob.IUseCase
	}
	workerRegistry struct {
		uc                  iUseCase
		laboratoriesEnabled bool
		labSweepInterval    time.Duration
	}
)

// DefaultLabSweepInterval is the period of the orphan lab group sweep when none is configured.
const DefaultLabSweepInterval = 10 * time.Minute

// NewWorkerRegistry receives the configuration-time fact only. It never probes
// the agent: a temporary unhealthy agent should still have its durable work
// retained for recovery, while no configured agent must create no lab jobs.
//
// labSweepInterval is the period of the orphan lab group sweep (a non-positive value means the default).
func NewWorkerRegistry(uc iUseCase, laboratoriesEnabled bool, labSweepInterval time.Duration) *workerRegistry {
	if labSweepInterval <= 0 {
		labSweepInterval = DefaultLabSweepInterval
	}
	return &workerRegistry{uc: uc, laboratoriesEnabled: laboratoriesEnabled, labSweepInterval: labSweepInterval}
}

// RegisterAll adds every job's worker to the bundle — one AddWorker line per job.
// Each job's args type T is inferred from its constructor's return, so AddWorker is
// called here per job (a heterogeneous-T slice of bare workers is not expressible in
// Go). Adding a job = import it, embed its port in useCase, add one line below.
func (wr *workerRegistry) RegisterAll(workers *river.Workers) {
	river.AddWorker(workers, notifyJob.NewWorker(wr.uc))
	river.AddWorker(workers, broadcastsendJob.NewWorker(wr.uc))
	river.AddWorker(workers, mediagcJob.NewWorker(wr.uc))
	river.AddWorker(workers, idempotencygcJob.NewWorker(wr.uc))
	river.AddWorker(workers, eventscoringpopulationJob.NewWorker(wr.uc))
	river.AddWorker(workers, eventformdeliveryJob.NewWorker(wr.uc))
	// Stands also prepare static challenges, so the engine runs even without
	// a configured Laboratory agent.
	river.AddWorker(workers, eventstandsJob.NewWorker(wr.uc))
	river.AddWorker(workers, agentmaintenanceJob.NewWorker(wr.uc))
	river.AddWorker(workers, resultchangegcJob.NewWorker(wr.uc))
	river.AddWorker(workers, eventmailJob.NewWorker(wr.uc))
	if wr.laboratoriesEnabled {
		river.AddWorker(workers, testdeploygcJob.NewWorker(wr.uc))
		river.AddWorker(workers, labcleanupJob.NewWorker(wr.uc))
		river.AddWorker(workers, labgroupsweepJob.NewWorker(wr.uc))
		river.AddWorker(workers, labaccesssyncJob.NewWorker(wr.uc))
	}
	river.AddWorker(workers, signalprocessingJob.NewWorker(wr.uc))
	river.AddWorker(workers, dataretentionJob.NewWorker(wr.uc))
	river.AddWorker(workers, accountinactivityJob.NewWorker(wr.uc))
	river.AddWorker(workers, eventanalyticsJob.NewWorker(wr.uc))
	river.AddWorker(workers, errorjournalJob.NewPurgeWorker(wr.uc))
	river.AddWorker(workers, resourcecalendarJob.NewWorker(wr.uc))
}

// PeriodicJobs declares the schedule-driven jobs (queried by the worker
// client at start).
func (wr *workerRegistry) PeriodicJobs() []*river.PeriodicJob {
	jobs := []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return jobsModel.MediaGCArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.IdempotencyGCArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.ResultChangeGCArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.EventScoringPopulationArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.EventFormDeliveryArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(2*time.Second), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.SignalProcessingArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(10*time.Second), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.EventStandsArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		// Event mail notices: one pass at a time, no retries (state lives in
		// the database; the next minute re-derives what is due).
		// Agent upkeep: certificates that end soon, retired access keys. Hourly, one pass at a time.
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.AgentMaintenanceArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.EventMailArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		// Data retention (Privacy Policy): batched purges every 6 hours, the
		// inactive-account warning/deletion pass daily. One pass at a time;
		// a failed pass is not retried, the next tick re-derives the work.
		river.NewPeriodicJob(river.PeriodicInterval(6*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.DataRetentionArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.AccountInactivityArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		// Error journal purge: groups, samples and 404 counters past the retention, once a day, one pass at a time.
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.ErrorJournalPurgeArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		// Event analytics rollups (5-minute buckets, VPN sessions): one pass a
		// minute, one at a time; the next tick re-derives what is due.
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.EventAnalyticsArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		// Resource calendar readiness check: alarms, completing the teams that had no agent, expired test lab
		// holds. One pass a minute, one at a time; the next tick re-derives everything.
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return jobsModel.ResourceCalendarArgs{}, standPassInsertOpts()
		}, &river.PeriodicJobOpts{RunOnStart: true}),
	}
	if wr.laboratoriesEnabled {
		jobs = append(jobs,
			river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.TestDeployGCArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
			river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.LabCleanupArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
			river.NewPeriodicJob(river.PeriodicInterval(2*time.Second), func() (river.JobArgs, *river.InsertOpts) { return jobsModel.LabAccessSyncArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
			// Orphan lab groups (a deleted event or team, an expired test lab): one pass at a time, never retried.
			river.NewPeriodicJob(river.PeriodicInterval(wr.labSweepInterval), func() (river.JobArgs, *river.InsertOpts) {
				return jobsModel.LabGroupSweepArgs{}, standPassInsertOpts()
			}, &river.PeriodicJobOpts{RunOnStart: false}),
		)
	}
	return jobs
}

// standPassInsertOpts keeps at most one pass queued or running, so a long
// pass is never overlapped by the next tick, and a failed pass is not retried
// (the next tick re-derives everything anyway). Shared by the stand, Event
// mail and data retention passes (unique by args, i.e. per job kind).
func standPassInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		MaxAttempts: 1,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
				rivertype.JobStateRetryable, rivertype.JobStateScheduled,
			},
		},
	}
}
