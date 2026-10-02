package app

import (
	"context"
	"github.com/cybericebox/daemon/internal/useCase/notification/startupdefaults"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller"
	"github.com/cybericebox/daemon/internal/delivery/infrastructure/agentfleet"
	"github.com/cybericebox/daemon/internal/delivery/infrastructure/labagent"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/infrastructureAgentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labPlacementRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labTrafficRepo"
	jobsRegistry "github.com/cybericebox/daemon/internal/jobs"
	errorjournalJob "github.com/cybericebox/daemon/internal/jobs/errorjournal"
	"github.com/cybericebox/daemon/internal/limits"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labMonitoring "github.com/cybericebox/daemon/internal/monitoring/lab"
	"github.com/cybericebox/daemon/internal/useCase"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/cybericebox/daemon/pkg/telegram"
	"github.com/cybericebox/daemon/pkg/worker"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

func Run(cfg *config.Config) {
	ctx := context.Background()
	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()

	// ── repository ──
	repo := repository.NewRepository(
		repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres},
	)
	defer repo.Close()

	// Migrations are a deliberate, separate step owned by the repository — not
	// pulled directly from postgres in the bootstrap.
	if err := repo.Migrate(); err != nil {
		log.Fatal().Err(err).Msg("Failed to run migrations")
	}

	// ── clients ──
	cls := setupClients(cfg)

	// job worker client
	// Every failed attempt, discard and panic of a job goes to the error journal.
	wc := worker.NewWorkerClient(repo.Pool(), worker.Retention{
		Completed: cfg.Tunables.JobCompletedRetention, Failed: cfg.Tunables.JobFailedRetention,
	}).WithErrorHandler(errorjournalJob.ErrorHandler{})

	// Flag rate limits are process-wide settings read by the submit paths.
	challengeAttempt.ChallengeRateLimit = challengeAttempt.RateLimit{Attempts: cfg.FlagRateLimit.ChallengeAttempts, Window: cfg.FlagRateLimit.ChallengeWindow}
	challengeAttempt.TeamRateLimit = challengeAttempt.RateLimit{Attempts: cfg.FlagRateLimit.TeamAttempts, Window: cfg.FlagRateLimit.TeamWindow}

	limits.Set(cfg.Limits)

	// The device resources settings were validated with the configuration.
	resourcesPolicy, err := cfg.Resources.Policy()
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid device resources settings")
	}

	// ── useCases ──
	deps := useCase.Dependencies{
		Repo:             repo,
		EnqueuerFactory:  wc,
		SMTPEnv:          cfg.Infrastructure.SMTP,
		OAuth:            cls.oauthClient,
		Storage:          cls.storageClient,
		Token:            cls.tokenClient,
		Password:         cls.passwordClient,
		AuthConfig:       cfg.Auth,
		MediaConfig:      cfg.Media,
		ExerciseConfig:   cfg.Exercise,
		ResourcesPolicy:  resourcesPolicy,
		Calendar:         calendarConfig(cfg.Calendar),
		SMTPAllowedPorts: cfg.Tunables.SMTPAllowedPorts,
		RetentionPolicy:  cfg.Retention.Policy(),
		ExerciseCipher:   cls.exerciseCipher,
		VPNCipher:        cls.vpnCipher,
		PlatformCipher:   cls.platformCipher,
		ErrorJournal:     errorJournalConfig(cfg),
		Telegram:         telegram.New(cfg.Telegram.BotToken),
	}
	applyTunables(cfg.Tunables)
	labIssuer, err := labaccess.New(labaccess.Config{
		TokenTTL: cfg.LabAccess.TokenTTL, SessionTTL: cfg.LabSession.TTL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to configure the lab access token issuer")
	}
	var ucs *useCase.UseCase // set below; the monitors start only after it exists
	// The infrastructure port is the agent fleet: every enrolled agent in the database. It reports
	// itself unavailable while it has no agent, so agents can be added in the admin without a restart.
	fleet := labagent.NewFleet(labPlacementRepo.New(repo.Queries), nil)
	fleet.SetPolicy(resourcesPolicy)
	trafficIngest := labMonitoring.NewTrafficIngest(labTrafficRepo.New(repo.Queries))
	observations := eventLabObservationRepo.New(repo.Queries)
	agentManager := agentfleet.New(agentfleet.Config{
		Instance: cfg.Infrastructure.Agent.InstanceID,
		Registry: infrastructureAgentRepo.New(repo.Queries),
		Cipher:   cipherOrNil(cls.platformCipher),
		Fleet:    fleet,
		Enroll:   labagent.Enroll,
		Monitor: func(ctx context.Context, member *labagent.Member) error {
			runner := labMonitoring.NewRunner(repo.Pool(), func(ctx context.Context, request *labpb.MonitoringRequest) (labMonitoring.Stream, error) {
				return member.Client.Monitoring(ctx, request)
			}, observations).
				WithSelector(member.Client.MonitoringSelector()).
				WithTraffic(trafficIngest)
			runner = runner.WithCapacitySink(func(ctx context.Context, capacity *labpb.CapacityResponse, observedAt time.Time) error {
				// The recorded capacity is the tenant quota; no quota means no limit on that resource.
				var cpu, memory *int64
				if capacity.GetHasCpuQuota() {
					v := capacity.GetCpuQuotaMillicores()
					cpu = &v
				}
				if capacity.GetHasMemoryQuota() {
					v := capacity.GetMemoryQuotaBytes()
					memory = &v
				}
				if err := ucs.AgentsUseCase.RecordAgentCapacity(ctx, member.ID, cpu, memory, observedAt); err != nil {
					return err
				}
				// The largest device the agent can place, for the calendar (an older agent reports none).
				return ucs.AgentsUseCase.RecordAgentMaxDevice(ctx, member.ID, labagent.MaxDeviceOf(capacity))
			})
			// What the agent offers (persistence, image cache, scheduler, endpoints): the live member gets it at
			// once, the registry keeps the last report.
			runner = runner.WithFeaturesSink(func(ctx context.Context, features *labpb.FeaturesResponse, observedAt time.Time) error {
				f := labagent.FeaturesOf(features)
				member.Features.Set(f)
				return ucs.AgentsUseCase.RecordAgentFeatures(ctx, member.ID, f, observedAt)
			})
			// The maintenance windows the cluster operator announces are read once a minute (the agent has no change
			// feed) while the agent is monitored; the calendar gives the agent no capacity inside them.
			pollCtx, stopPolling := context.WithCancel(ctx)
			defer stopPolling()
			go labagent.PollMaintenance(pollCtx, member.Name, labagent.MaintenanceInterval,
				func(ctx context.Context) (*labpb.MaintenanceWindowList, error) {
					return member.Client.ListMaintenanceWindows(ctx, &labpb.ListMaintenanceWindowsRequest{})
				},
				func(ctx context.Context, windows []infraModel.AgentMaintenanceWindow) error {
					return ucs.AgentsUseCase.RecordAgentMaintenance(ctx, member.ID, windows)
				})
			// Everything stored carries the registry id of the agent.
			runner = runner.WithErrorsSink(func(ctx context.Context, report *labpb.ErrorJournal) {
				ucs.Journal.ReportAgentErrors(ctx, member.Name, agentErrors(report))
			})
			runner = runner.WithLinkSink(agentLink{journal: ucs.Journal, id: member.ID, name: member.Name})
			return runner.WithAgentID(member.ID.String()).Run(ctx)
		},
	})
	deps.LabAgent = fleet
	deps.AgentFleet = agentManager
	deps.AgentRemote = agentManager
	// Web links are signed with the access key of the agent that holds the lab group.
	deps.LabSessions = labagent.SessionIssuer{Fleet: fleet, Issuer: labIssuer}
	ucs = useCase.NewUseCase(deps)
	// The error journal: capture points anywhere in the process report to it without waiting; its writer and its
	// periodic checks (job queue, agent certificates) run until shutdown.
	errorJournal.SetReporter(ucs.Journal)
	journalDone := make(chan struct{})
	go func() {
		defer close(journalDone)
		ucs.Journal.Run(runtimeCtx)
	}()
	go ucs.Journal.Watch(runtimeCtx)
	bootstrapAgent(ctx, cfg.Infrastructure.Agent, ucs.AgentsUseCase)

	if err := startupdefaults.Seed(ctx, repo.Queries); err != nil {
		log.Error().Err(err).Msg("Failed to seed the startup notification defaults")
	}
	// ── bootstrap: promote designated super-admin if the account already exists ──
	if err := ucs.PromoteSuperAdminIfDesignated(ctx); err != nil {
		log.Error().Err(err).Msg("Failed to promote designated super admin")
	}
	// job worker registry
	wr := jobsRegistry.NewWorkerRegistry(ucs, true)

	// ── jobs ──
	// The full ucs aggregate is handed to every worker registrar; each worker
	// narrows it via worker's hidden useCase port (e.g., ProcessNotification).
	// Late-bind the enqueuer's client so Notify can enqueue jobs after River starts.
	wc.Initialize(ctx, wr)

	// ── delivery ──
	ctrl := controller.NewController(
		controller.Dependencies{
			UseCase:              ucs,
			HTTPControllerConfig: cfg.HTTPController,
			AuthConfig:           cfg.Auth,
			RateLimit:            cfg.RateLimit,
			ErrorJournal:         ucs.Journal,
		},
	)
	ctrl.Start()
	log.Info().Msg("Server started")

	// The agent manager loads the fleet, follows the registry and runs one monitoring stream per agent.
	monitoringDone := make(chan struct{})
	go func() {
		defer close(monitoringDone)
		agentManager.Run(runtimeCtx)
	}()

	// ── graceful shutdown ──
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutting down server")
	stopRuntime()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wc.Stop(shutdownCtx)
	ctrl.Stop(shutdownCtx)
	// The writer drains what the last requests and jobs reported.
	select {
	case <-journalDone:
	case <-shutdownCtx.Done():
		log.Warn().Msg("Timed out flushing the error journal")
	}
	select {
	case <-monitoringDone:
	case <-shutdownCtx.Done():
		log.Warn().Msg("Timed out stopping laboratory monitoring")
	}
}

// calendarConfig hands the resource calendar settings to the use case.
func calendarConfig(c config.CalendarConfig) calendarUseCase.Config {
	return calendarUseCase.Config{
		BufferPercent: c.BufferPercent, TailGap: c.TailGap, LeadMargin: c.LeadMargin, SearchHorizon: c.SearchHorizon,
		AgentFresh: c.AgentFresh, TestLabLease: c.TestLabLease,
	}
}
