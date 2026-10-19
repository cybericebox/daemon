package useCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/errorJournalRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventActivityRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	eventFormRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/infrastructureAgentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/jobQueueRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labPlacementRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labTrafficRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/signalOutboxRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	"github.com/cybericebox/daemon/internal/session"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	infrastructureUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	mediaUseCase "github.com/cybericebox/daemon/internal/useCase/media"
	bannerUseCase "github.com/cybericebox/daemon/internal/useCase/notification/banner"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/inapp"
	"github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/internal/useCase/notification/settings"
	statsUseCase "github.com/cybericebox/daemon/internal/useCase/notification/stats"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
	"github.com/cybericebox/daemon/internal/useCase/platformSettings"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
	retentionUseCase "github.com/cybericebox/daemon/internal/useCase/retention"
	signalUseCase "github.com/cybericebox/daemon/internal/useCase/signal"
	vpnUseCase "github.com/cybericebox/daemon/internal/useCase/vpn"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/storage"
	"github.com/cybericebox/daemon/pkg/token"
	"github.com/cybericebox/daemon/pkg/worker"
)

type (
	UseCase struct {
		*authUseCase.AuthUseCase
		*adminAuditUseCase.UseCase
		*platformSettingsUseCase.PlatformSettingsUseCase
		*dispatcherUseCase.NotificationDispatcher
		*inboxUseCase.NotificationInboxUseCase
		*settingsUseCase.NotificationSettingsUseCase
		*emailUseCase.NotificationEmailTemplateUseCase
		*emailUseCase.NotificationEmailPresetUseCase
		*inAppUseCase.NotificationInAppTemplateUseCase
		*statsUseCase.NotificationStatsUseCase
		*mediaUseCase.MediaUseCase
		*exerciseUseCase.ExerciseUseCase
		*eventUseCase.EventUseCase
		*infrastructureUseCase.InfrastructureUseCase
		*infrastructureUseCase.TestLabsUseCase
		*infrastructureUseCase.AgentsUseCase
		*signalUseCase.Processor
		*mailUseCase.MailUseCase
		*retentionUseCase.RetentionUseCase
		*eventAnalyticsUseCase.EventAnalyticsUseCase
		*platformAnalyticsUseCase.PlatformAnalyticsUseCase
		*broadcastUseCase.NotificationBroadcastUseCase
		*bannerUseCase.SiteBannerUseCase
		*errorJournalUseCase.Journal
		*calendarUseCase.ResourceCalendarUseCase
		*imageRegistryUseCase.ImageRegistryUseCase
	}
	Dependencies struct {
		Repo            *repository.Repository
		EnqueuerFactory worker.IEnqueuerFactory
		// SMTPEnv is the bootstrap SMTP_* transport (used while the database
		// holds no platform mail settings).
		SMTPEnv    config.SMTPConfig
		OAuth      *oauth.Client
		Storage    *storage.Client
		Token      *token.Client
		Password   *password.Client
		AuthConfig config.AuthConfig
		// Sessions is the cookie codec, the revoked-session set and the last_seen batching of this replica.
		Sessions           *session.Runtime
		MediaConfig        config.MediaConfig
		LabLifecycleConfig config.LabLifecycleConfig
		ExerciseConfig     config.ExerciseConfig
		// ResourcesPolicy is the platform's device resources settings (presets, frame, ceiling); zero: the
		// owner's defaults.
		ResourcesPolicy resourcesModel.Policy
		// SMTPAllowedPorts are the ports an organizer may use for an event SMTP server.
		SMTPAllowedPorts []int
		// RetentionPolicy is the Privacy Policy's retention periods: the defaults the platform settings override.
		RetentionPolicy retentionModel.Policy
		// One cipher per secret family (separate keys); each nil when its key is unset.
		ExerciseCipher *secret.Cipher // exercise secret env vars
		VPNCipher      *secret.Cipher // VPN client configs
		PlatformCipher *secret.Cipher // platform settings secrets (SMTP, image registry tokens)
		// LabAgent is the infrastructure port, the agent fleet; nil in tests without infrastructure.
		LabAgent infrastructureUseCase.Agent
		// AgentFleet applies the admin-configured agents to the live fleet and probes them; nil without one.
		AgentFleet infrastructureUseCase.AgentFleet
		// AgentRemote enrolls agents and keeps their certificates and access keys; nil without one.
		AgentRemote infrastructureUseCase.AgentRemote
		// LabSessions signs the tokens of the laboratory L7 proxy; nil when unconfigured.
		LabSessions eventUseCase.LabSessionIssuer
		// Calendar tunes the resource calendar (event reservation buffer, tail gap, lead margin).
		Calendar calendarUseCase.Config
		// ErrorJournal tunes the platform error journal; Telegram is its bot (nil or disabled: no Telegram messages).
		ErrorJournal errorJournalUseCase.Config
		Telegram     errorJournalUseCase.Telegram
	}
)

// Both embedded infrastructure and exercise use cases expose this capability.
// Resolve the promoted-method ambiguity for the controller's exercise port.
func (u *UseCase) InfrastructureAvailable() bool {
	return u.ExerciseUseCase.InfrastructureAvailable()
}

// ArchiveLimits resolves the promoted-method ambiguity: both embedded platform settings and exercise use cases have
// it, and the controller's exercise port takes the exercise one (the limits in force, the defaults when the settings
// cannot be read).
func (u *UseCase) ArchiveLimits(ctx context.Context) exerciseModel.ArchiveLimits {
	return u.ExerciseUseCase.ArchiveLimits(ctx)
}

// buildNotificationHandlers assembles the channel handlers the dispatcher routes
// to. Both are always wired: the mail use case resolves the SMTP transport per
// message (database settings, else SMTP_*), and a message with no transport is
// journaled as an error. repo (the aggregate data port) satisfies each
// handler's narrow read port structurally; media serves the uploaded images the
// email handler embeds as inline parts.
func buildNotificationHandlers(
	repo *repository.Repository,
	mail *mailUseCase.MailUseCase,
	media *mediaUseCase.MediaUseCase,
	brands emailUseCase.EventBrandResolver,
) []dispatcherUseCase.Handler {
	return []dispatcherUseCase.Handler{
		inAppUseCase.NewHandler(repo),
		emailUseCase.NewHandler(repo, mail, media, brands),
	}
}

// broadcastEnqueue adapts the River enqueuer to the broadcast sender's port.
func broadcastEnqueue(enq worker.IEnqueuer) func(ctx context.Context, args jobsModel.BroadcastSendArgs) error {
	return func(ctx context.Context, args jobsModel.BroadcastSendArgs) error { return enq.Enqueue(ctx, args) }
}

// blobPushQueue adapts the River enqueuer to the image upload's blob push port: one push per digest while queued
// or running.
func blobPushQueue(enq worker.IEnqueuer) imageRegistryUseCase.BlobPushQueue {
	return func(ctx context.Context, args jobsModel.ImageBlobPushArgs) error {
		return enq.EnqueueUniqueAt(ctx, args, time.Now())
	}
}

// importQueue adapts the River enqueuer to the background archive import: one run per job while queued or running.
func importQueue(enq worker.IEnqueuer) exerciseUseCase.ImportJobQueue {
	return func(ctx context.Context, args jobsModel.ExerciseImportArgs) error {
		return enq.EnqueueUniqueAt(ctx, args, time.Now())
	}
}

// exportQueue adapts the River enqueuer to the background export «з образами»: one run per job while queued or running.
func exportQueue(enq worker.IEnqueuer) exerciseUseCase.ExportJobQueue {
	return func(ctx context.Context, args jobsModel.ExerciseExportArgs) error {
		return enq.EnqueueUniqueAt(ctx, args, time.Now())
	}
}

// imageExporter adapts the image registry use case to the export's port.
type imageExporter struct {
	uc *imageRegistryUseCase.ImageRegistryUseCase
}

func (e imageExporter) ExportedImage(ctx context.Context, reference string) (exerciseUseCase.ExportedImage, error) {
	image, err := e.uc.ResolveImageExport(ctx, reference)
	if err != nil {
		return nil, err
	}
	return image, nil
}

// testDeployExpiry adapts the River enqueuer to the exercise use case's test lab expiry port.
type testDeployExpiry struct{ enq worker.IEnqueuer }

func (t testDeployExpiry) ScheduleTestDeployExpiry(ctx context.Context, ownerID, deployID uuid.UUID, at time.Time) error {
	return t.enq.EnqueueAt(ctx, jobsModel.TestDeployExpiryArgs{DeployID: deployID, OwnerID: ownerID}, at)
}

// ScheduleTestDeployRemovalCheck queues the next follow-up check of a lab being removed; one waits per deploy.
func (t testDeployExpiry) ScheduleTestDeployRemovalCheck(ctx context.Context, ownerID, deployID uuid.UUID, attempt int, at time.Time) error {
	return t.enq.EnqueueUniqueAt(ctx, jobsModel.TestDeployRemovalCheckArgs{DeployID: deployID, OwnerID: ownerID, Attempt: attempt}, at)
}

func NewUseCase(deps Dependencies) *UseCase {
	// The platform settings come first: the retention periods, the archive limits and the test run leases saved in
	// them are read live by the use cases below (the env values are their defaults).
	platformSettingsUC := platformSettingsUseCase.NewPlatformSettingsUseCase(
		platformSettingsUseCase.Dependencies{
			Repo:              deps.Repo,
			RetentionDefaults: deps.RetentionPolicy,
			TestRunDefaults:   exerciseModel.TestRunLeases{Lease: deps.ExerciseConfig.TestDeployTTL, Max: deps.ExerciseConfig.TestDeployTTLMax},
			MaxUploadBytes:    deps.MediaConfig.MaxUploadBytes,
		},
	)

	// mediaUC is built first: the email handler streams uploaded images from it
	// (inline CID parts), and the aggregate exposes it for the periodic GC job.
	mediaUC := mediaUseCase.NewMediaUseCase(
		mediaUseCase.Dependencies{
			Repo:    deps.Repo,
			Storage: deps.Storage,
			Config:  deps.MediaConfig,
		},
	)

	vpnUC := vpnUseCase.NewVPNUseCase(
		vpnUseCase.Dependencies{Repo: deps.Repo, Cipher: deps.VPNCipher},
	)

	// The same issuer signs the test-deploy tokens; nil when no proxy key is set.
	testSessions, _ := deps.LabSessions.(exerciseUseCase.ITestSessions)
	exerciseUC := exerciseUseCase.NewExerciseUseCase(
		exerciseUseCase.Dependencies{
			Repo:   deps.Repo,
			Cipher: deps.ExerciseCipher,
			Media:  mediaUC,
			// deps.LabAgent is a nil interface when infrastructure is absent, so
			// the exercise use case reports the test deploy as unavailable.
			Infra:      deps.LabAgent,
			VPNStore:   vpnUC,
			FlagConfig: deps.ExerciseConfig,
			Sessions:   testSessions,
			DeployUoW:  postgres.NewUnitOfWorker[testDeployRepo.Queries](deps.Repo.UoWFactory()),
			Resources:  deps.ResourcesPolicy,
			Elevations: exerciseRepo.New(deps.Repo),
			// The per-task file store follows the media GC: a file past this window with no reference is gone soon.
			FileGCGrace: deps.MediaConfig.GCGrace,
			Settings:    platformSettingsUC,
		},
	)
	exerciseUC.SetVPNConfigAudit(eventActivityRepo.New(deps.Repo))
	// A task archive upload may be as large as the archive limit in force plus one image archive.
	mediaUC.SetArchiveUploadLimit(func(ctx context.Context) int64 { return exerciseUC.ArchiveLimits(ctx).WithImagesBytes() })

	infraUC := infrastructureUseCase.NewInfrastructureUseCase(
		infrastructureUseCase.Dependencies{Agent: deps.LabAgent, Agents: infrastructureAgentRepo.New(deps.Repo), Observations: eventLabObservationRepo.New(deps.Repo), Stands: platformStandRepo.New(deps.Repo)},
	)

	testLabsUC := infrastructureUseCase.NewTestLabsUseCase(
		infrastructureUseCase.TestLabsDependencies{Source: platformStandRepo.New(deps.Repo), Agent: deps.LabAgent, Destroyer: exerciseUC},
	)

	// Setup links of the sign-up, Google and invitation flows share one store: a new link for an
	// account replaces its old one, finishing the setup spends it.
	setupTokens := authUseCase.NewSetupTokenStore(deps.Repo, deps.Token)

	eventUC := eventUseCase.NewEventUseCase(
		eventUseCase.Dependencies{
			LabLifecycleBatch:        deps.LabLifecycleConfig.Batch,
			LabLifecycleRetryMin:     deps.LabLifecycleConfig.RetryMin,
			LabLifecycleRetryMax:     deps.LabLifecycleConfig.RetryMax,
			Repo:                     deps.Repo,
			UoW:                      postgres.NewUnitOfWorker[eventUseCase.IRepository](deps.Repo.UoWFactory()),
			Infra:                    deps.LabAgent,
			InfrastructureCapability: infraUC,
			Topologies:               exerciseUC,
			PreviewLabs:              exerciseUC,
			VPN:                      vpnUC,
			SignalPublishers:         eventUseCase.NewOutboxSignalPublisherFactory(time.Now),
			FlagRandomBytes:          deps.ExerciseConfig.FlagRandomBytes,
			StandDeployBudget:        deps.ExerciseConfig.StandDeployBudget,
			StandPrewarmLead:         deps.ExerciseConfig.StandPrewarmLead,
			LabSweepGrace:            deps.ExerciseConfig.LabSweepGrace,
			Media:                    mediaUC,
			BrandMedia:               mediaUC,
			PublicAPIBaseURL:         deps.AuthConfig.Hosts.APIURL(""),
			EventDomain:              deps.AuthConfig.Hosts.EventDomain,
			IDHost:                   deps.AuthConfig.Hosts.ID,
			SetupTokens:              setupTokens,
			LabSessions:              deps.LabSessions,
			Resources:                deps.ResourcesPolicy,
			Elevations:               exerciseRepo.New(deps.Repo),
		},
	)

	// The resource calendar: reservations of lab resources over time, placed over the agents by packing. It plans
	// from the event's resource plan, reads the agents' recorded capacity and tells the platform admins through the
	// inbox; the event use case asks it before a new task of a running event, the exercise use case before a test
	// laboratory, and an agent's deletion lists the reservations it would leave not covered.
	calendarUC := calendarUseCase.New(calendarUseCase.Dependencies{
		Store: resourceCalendarRepo.New(deps.Repo), Tx: calendarTx{uow: postgres.NewUnitOfWorker[resourceCalendarRepo.Queries](deps.Repo.UoWFactory())},
		Agents: infrastructureAgentRepo.New(deps.Repo), Events: eventRepo.New(deps.Repo), Configs: eventConfigRepo.New(deps.Repo),
		Planner: calendarNeeds{events: eventUC}, Usage: calendarUsage{observations: eventLabObservationRepo.New(deps.Repo), allocations: eventLabAllocationRepo.New(deps.Repo), events: eventUC},
		Config: deps.Calendar, Frame: deps.ResourcesPolicy.Frame, Policy: deps.ResourcesPolicy, Overhead: groupOverhead(deps.LabAgent),
	})
	eventUC.SetResourceGate(calendarUC)
	eventUC.SetAllocationAccounting(true)
	eventUC.SetLifecycleControls(true)
	eventUC.SetNoticeCanceller(jobQueueRepo.New(deps.Repo.Limited()).CancelEventNotifications)
	exerciseUC.SetTestLabGate(calendarUC)
	// A room check naming no lease answers for the lease in force (the platform settings), like the admission.
	calendarUC.SetTestLabLease(exerciseUC.TestLabLease)
	// The calendar's reservations are split per agent and sent to the agents' reservation API.
	if port, ok := deps.LabAgent.(interface {
		infraModel.ReservationPort
		ReservationNotice(agentID uuid.UUID) time.Duration
	}); ok {
		calendarUC.SetClusterReservations(port, port.ReservationNotice)
	}
	// Placement follows the parts: a new group goes to the agent of its reservation's unit and every group and lab
	// carries its part id.
	if fleet, ok := deps.LabAgent.(interface{ SetPartResolver(infraModel.PartResolver) }); ok {
		fleet.SetPartResolver(calendarUC)
	}
	// The lab jobs exist only with an infrastructure agent (see jobsRegistry); without one nothing is scheduled.
	if deps.LabAgent != nil {
		labJobs := deps.EnqueuerFactory.NewEnqueuer()
		exerciseUC.SetTestDeployExpiry(testDeployExpiry{enq: labJobs})
		exerciseUC.SetTestDeployRemovalCheck(testDeployExpiry{enq: labJobs})
		eventUC.SetLabLifecycleWake(func(ctx context.Context) error { return labJobs.Enqueue(ctx, jobsModel.LabLifecycleArgs{}) })
		eventUC.SetLabCleanupWake(func(ctx context.Context) error { return labJobs.Enqueue(ctx, jobsModel.LabCleanupArgs{}) })
	}

	var agentSealer infrastructureUseCase.AgentSealer
	// The registry settings work without a cipher (saving tokens is refused); a nil *Cipher must not become a
	// non-nil interface.
	var registrySealer secret.Sealer
	if deps.PlatformCipher != nil {
		agentSealer = deps.PlatformCipher
		registrySealer = deps.PlatformCipher
	}
	imageRegistryUC := imageRegistryUseCase.New(imageRegistryUseCase.Dependencies{
		Repo: deps.Repo, Cipher: registrySealer, Media: mediaUC, QueueBlobPush: blobPushQueue(deps.EnqueuerFactory.NewEnqueuer()),
	})
	// Every lab deploy and prewarm checks its images first and sends exactly that lab's pull credentials.
	exerciseUC.SetImageAccess(imageRegistryUC)
	// Task archives naming device image files import in the background: images pushed through the registry use
	// case, the archive read where it lies, the password sealed with the platform cipher.
	exerciseUC.SetArchiveImport(exerciseUseCase.ArchiveImportDependencies{
		Images: imageRegistryUC, Sources: mediaUC, Queue: importQueue(deps.EnqueuerFactory.NewEnqueuer()), Sealer: registrySealer,
	})
	// Exports «з образами» run in the background too: images read from the registry, the zip stored as a media file.
	exerciseUC.SetArchiveExport(exerciseUseCase.ArchiveExportDependencies{
		Images: imageExporter{uc: imageRegistryUC}, Files: mediaUC, Queue: exportQueue(deps.EnqueuerFactory.NewEnqueuer()), Sealer: registrySealer,
	})
	eventUC.SetImageAccess(imageRegistryUC)
	agentsUC := infrastructureUseCase.NewAgentsUseCase(infrastructureUseCase.AgentsDependencies{
		Store: infrastructureAgentRepo.New(deps.Repo), Placements: labPlacementRepo.New(deps.Repo),
		Sealer: agentSealer, Fleet: deps.AgentFleet, Remote: deps.AgentRemote, Frame: deps.ResourcesPolicy.Frame,
		Impacts: calendarUC,
	})

	mailUC := mailUseCase.NewMailUseCase(mailUseCase.Dependencies{
		Repo: deps.Repo, Cipher: deps.PlatformCipher, Env: deps.SMTPEnv, SMTPPolicy: mailModel.SMTPPolicy{AllowedPorts: deps.SMTPAllowedPorts}, Domain: deps.AuthConfig.Hosts.Main, SupportEmail: deps.AuthConfig.SupportEmail,
	})
	handlers := buildNotificationHandlers(deps.Repo, mailUC, mediaUC, eventUC)
	notificationDispatcher := dispatcherUseCase.NewNotificationDispatcher(
		dispatcherUseCase.Dependencies{
			UnitOfWorker: postgres.NewUnitOfWorker[dispatcherUseCase.Repository](deps.Repo.UoWFactory()),
			Repo:         deps.Repo,
			Enqueuer:     deps.EnqueuerFactory.NewEnqueuer(),
			Handlers:     handlers,
			Cipher:       deps.PlatformCipher,
		},
	)
	eventUC.SetInvitationNotifier(notificationDispatcher)
	// Inbox requests that reach every person who may act on them (Event
	// managers, platform admins) and close for all of them on decision.
	inboxRequests := inboxUseCase.NewRequestRouter(deps.Repo, notificationDispatcher)
	inboxRequests.SetAdminURL(deps.AuthConfig.Hosts.AdminURL("/"))
	eventUC.SetStandInbox(inboxRequests)
	exerciseUC.SetProposalInbox(inboxRequests)
	exerciseUC.SetElevationInbox(inboxRequests)
	exerciseUC.SetImportInbox(inboxRequests)
	calendarUC.SetNotifier(inboxRequests)
	// The periodic registry credential check tells the people who can fix a credential when it breaks or recovers.
	imageRegistryUC.SetCheckNotifier(inboxRequests)
	eventUC.SetEmailFooters(mailUC)
	emailTemplateUC := emailUseCase.NewNotificationEmailTemplateUseCase(deps.Repo, mediaUC)
	emailTemplateUC.SetFooterSource(mailUC)

	// The registries are intentionally code-owned. Payload registrations and
	// concrete hooks are added alongside their emitting domains; no signal is
	// published until that registration exists.
	signalHooks := signalModel.NewHookRegistry()
	signalHooks.RegisterWildcard(eventUseCase.NewFormDeliveryHook(
		eventUseCase.NewFormDeliveryRepositoryStore(eventFormRepo.New(deps.Repo), time.Now),
		signalModel.DefaultRegistry,
	))
	for _, typ := range inboxRequests.Signals() {
		signalHooks.RegisterExact(typ, inboxRequests)
	}
	signalUseCase.SetEventSiteDomain(deps.AuthConfig.Hosts.EventDomain)
	signalHooks.RegisterWildcard(signalUseCase.NewNotificationPlanner(deps.Repo, signalModel.DefaultRegistry, notificationDispatcher))
	signalProcessor := signalUseCase.NewProcessor(
		signalOutboxRepo.NewExecutionStore(deps.Repo),
		signalModel.DefaultRegistry,
		signalHooks,
		time.Now,
	)

	authUC := authUseCase.NewAuthUseCase(
		authUseCase.Dependencies{
			Repo:     deps.Repo,
			Token:    deps.Token,
			Password: deps.Password,
			Notifier: notificationDispatcher,
			OAuth:    deps.OAuth,
			Storage:  deps.Storage,
			Avatar:   mediaUC,
			Config:   deps.AuthConfig,
			Sessions: deps.Sessions,

			SetupTokens: setupTokens,
		},
	)
	authUC.SetDeliveryQueue(deps.EnqueuerFactory.NewEnqueuer(), deps.PlatformCipher, postgres.NewUnitOfWorker[authUseCase.IRepository](deps.Repo.UoWFactory()))
	authUC.SetInboxRequests(inboxRequests)

	// Inactive accounts leave through the same cascade as a deletion request.
	retentionUC := retentionUseCase.New(retentionUseCase.Dependencies{
		Store:     retentionRepo.New(deps.Repo),
		Notifier:  notificationDispatcher,
		Accounts:  authUC,
		Policies:  platformSettingsUC,
		SignInURL: deps.AuthConfig.Hosts.IDURL("/sign-in/"),
	})

	journalUC := errorJournalUseCase.New(errorJournalUseCase.Dependencies{
		Repo: errorJournalRepo.New(
			deps.Repo.Queries,
			deps.Repo.Limited(),
			deps.EnqueuerFactory.NewEnqueuer(),
		),
		Telegram: deps.Telegram,
		Mailer:   mailUC,
		Agents:   errorJournalUseCase.CertificatesOf(infrastructureAgentRepo.New(deps.Repo)),
		Config:   deps.ErrorJournal,
	})

	return &UseCase{
		authUC,
		adminAuditUseCase.New(deps.Repo),
		platformSettingsUC,
		notificationDispatcher,
		inboxUseCase.NewNotificationInboxUseCase(
			inboxUseCase.Dependencies{Repo: deps.Repo},
		),
		settingsUseCase.NewNotificationSettingsUseCase(
			settingsUseCase.Dependencies{Repo: deps.Repo},
		),
		emailTemplateUC,
		emailUseCase.NewNotificationEmailPresetUseCase(deps.Repo, mediaUC),
		inAppUseCase.NewNotificationInAppTemplateUseCase(deps.Repo),
		statsUseCase.NewNotificationStatsUseCase(statsUseCase.Dependencies{Repo: deps.Repo}),
		mediaUC,
		exerciseUC,
		eventUC,
		infraUC,
		testLabsUC,
		agentsUC,
		signalProcessor,
		mailUC,
		retentionUC,
		eventAnalyticsUseCase.New(eventAnalyticsUseCase.Dependencies{
			Store:       eventAnalyticsRepo.New(deps.Repo),
			Events:      eventRepo.New(deps.Repo),
			Configs:     eventConfigRepo.New(deps.Repo),
			Memberships: eventManagerRepo.New(deps.Repo),
			LabTraffic:  labTrafficRepo.New(deps.Repo),
		}),
		platformAnalyticsUseCase.New(platformAnalyticsUseCase.Dependencies{Store: platformAnalyticsRepo.New(deps.Repo), TestLabResources: testLabsUC.TotalResources}),
		broadcastUseCase.NewNotificationBroadcastUseCase(broadcastUseCase.Dependencies{
			Repo: deps.Repo, Notifier: notificationDispatcher, Enqueue: broadcastEnqueue(deps.EnqueuerFactory.NewEnqueuer()),
			UnitOfWorker: postgres.NewUnitOfWorker[broadcastUseCase.Repository](deps.Repo.UoWFactory()), Enqueuer: deps.EnqueuerFactory.NewEnqueuer(),
			EventDomain: deps.AuthConfig.Hosts.EventDomain,
			Images:      emailUseCase.NewBroadcastImages(mediaUC, emailTemplateRepo.New(deps.Repo), emailTemplateRepo.New(deps.Repo)),
		}),
		bannerUseCase.NewSiteBannerUseCase(bannerUseCase.Dependencies{Repo: deps.Repo}),
		journalUC,
		calendarUC,
		imageRegistryUC,
	}
}
