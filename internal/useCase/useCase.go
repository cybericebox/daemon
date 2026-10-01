package useCase

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	eventFormRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/infrastructureAgentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labPlacementRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labTrafficRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/signalOutboxRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
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
	}
	Dependencies struct {
		Repo            *repository.Repository
		EnqueuerFactory worker.IEnqueuerFactory
		// SMTPEnv is the bootstrap SMTP_* transport (used while the database
		// holds no platform mail settings).
		SMTPEnv        config.SMTPConfig
		OAuth          *oauth.Client
		Storage        *storage.Client
		Token          *token.Client
		Password       *password.Client
		AuthConfig     config.AuthConfig
		MediaConfig    config.MediaConfig
		ExerciseConfig config.ExerciseConfig
		// RetentionPolicy is the Privacy Policy's retention periods.
		RetentionPolicy retentionModel.Policy
		// One cipher per secret family (separate keys); each nil when its key is unset.
		ExerciseCipher *secret.Cipher // exercise secret env vars
		VPNCipher      *secret.Cipher // VPN client configs
		PlatformCipher *secret.Cipher // platform settings secrets (SMTP)
		// LabAgent is the infrastructure port, the agent fleet; nil in tests without infrastructure.
		LabAgent infrastructureUseCase.Agent
		// AgentFleet applies the admin-configured agents to the live fleet and probes them; nil without one.
		AgentFleet infrastructureUseCase.AgentFleet
		// AgentRemote enrolls agents and keeps their certificates and access keys; nil without one.
		AgentRemote infrastructureUseCase.AgentRemote
		// LabSessions signs the tokens of the laboratory L7 proxy; nil when unconfigured.
		LabSessions eventUseCase.LabSessionIssuer
	}
)

// Both embedded infrastructure and exercise use cases expose this capability.
// Resolve the promoted-method ambiguity for the controller's exercise port.
func (u *UseCase) InfrastructureAvailable() bool {
	return u.ExerciseUseCase.InfrastructureAvailable()
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

func NewUseCase(deps Dependencies) *UseCase {
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
		},
	)

	infraUC := infrastructureUseCase.NewInfrastructureUseCase(
		infrastructureUseCase.Dependencies{Agent: deps.LabAgent, Agents: infrastructureAgentRepo.New(deps.Repo), Observations: eventLabObservationRepo.New(deps.Repo), Stands: platformStandRepo.New(deps.Repo)},
	)

	var agentSealer infrastructureUseCase.AgentSealer
	if deps.PlatformCipher != nil {
		agentSealer = deps.PlatformCipher
	}
	agentsUC := infrastructureUseCase.NewAgentsUseCase(infrastructureUseCase.AgentsDependencies{
		Store: infrastructureAgentRepo.New(deps.Repo), Placements: labPlacementRepo.New(deps.Repo),
		Sealer: agentSealer, Fleet: deps.AgentFleet, Remote: deps.AgentRemote,
	})

	testLabsUC := infrastructureUseCase.NewTestLabsUseCase(
		infrastructureUseCase.TestLabsDependencies{Source: platformStandRepo.New(deps.Repo), Agent: deps.LabAgent, Destroyer: exerciseUC},
	)

	eventUC := eventUseCase.NewEventUseCase(
		eventUseCase.Dependencies{
			Repo:                     deps.Repo,
			UoW:                      postgres.NewUnitOfWorker[eventUseCase.IRepository](deps.Repo.UoWFactory()),
			Infra:                    deps.LabAgent,
			InfrastructureCapability: infraUC,
			Topologies:               exerciseUC,
			VPN:                      vpnUC,
			SignalPublishers:         eventUseCase.NewOutboxSignalPublisherFactory(time.Now),
			FlagRandomBytes:          deps.ExerciseConfig.FlagRandomBytes,
			StandDeployBudget:        deps.ExerciseConfig.StandDeployBudget,
			StandPrewarmLead:         deps.ExerciseConfig.StandPrewarmLead,
			Media:                    mediaUC,
			BrandMedia:               mediaUC,
			PublicAPIBaseURL:         deps.AuthConfig.Hosts.APIURL(""),
			EventDomain:              deps.AuthConfig.Hosts.EventDomain,
			IDHost:                   deps.AuthConfig.Hosts.ID,
			SetupTokens:              deps.Token,
			LabSessions:              deps.LabSessions,
		},
	)

	mailUC := mailUseCase.NewMailUseCase(mailUseCase.Dependencies{
		Repo: deps.Repo, Cipher: deps.PlatformCipher, Env: deps.SMTPEnv, Domain: deps.AuthConfig.Hosts.Main,
	})
	handlers := buildNotificationHandlers(deps.Repo, mailUC, mediaUC, eventUC)
	notificationDispatcher := dispatcherUseCase.NewNotificationDispatcher(
		dispatcherUseCase.Dependencies{
			Repo:     deps.Repo,
			Enqueuer: deps.EnqueuerFactory.NewEnqueuer(),
			Handlers: handlers,
		},
	)
	eventUC.SetInvitationNotifier(notificationDispatcher)
	// Inbox requests that reach every person who may act on them (Event
	// managers, platform admins) and close for all of them on decision.
	inboxRequests := inboxUseCase.NewRequestRouter(deps.Repo, notificationDispatcher)
	eventUC.SetStandInbox(inboxRequests)
	exerciseUC.SetProposalInbox(inboxRequests)
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
		},
	)
	authUC.SetInboxRequests(inboxRequests)

	// Inactive accounts leave through the same cascade as a deletion request.
	retentionUC := retentionUseCase.New(retentionUseCase.Dependencies{
		Store:     retentionRepo.New(deps.Repo),
		Notifier:  notificationDispatcher,
		Accounts:  authUC,
		Policy:    deps.RetentionPolicy,
		SignInURL: deps.AuthConfig.Hosts.IDURL("/sign-in"),
	})

	return &UseCase{
		authUC,
		adminAuditUseCase.New(deps.Repo),
		platformSettingsUseCase.NewPlatformSettingsUseCase(
			platformSettingsUseCase.Dependencies{Repo: deps.Repo},
		),
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
			EventDomain: deps.AuthConfig.Hosts.EventDomain,
		}),
		bannerUseCase.NewSiteBannerUseCase(bannerUseCase.Dependencies{Repo: deps.Repo}),
	}
}
