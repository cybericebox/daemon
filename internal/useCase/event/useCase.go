// Package event implements the platform Event tenancy application layer:
// identity CRUD, the event config (participant-visible settings, 1:1 with an
// Event), and their mutateEvent/mutateEventConfig optimistic-lock write paths.
package event

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cybericebox/daemon/internal/delivery/repository/challengeAttemptRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventActivityRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnswerFileRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventContentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventNotificationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/inAppTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/liveScreenRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/mailRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/requestIdempotencyRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/scoreboardRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/signalOutboxRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	eventManagerUseCase "github.com/cybericebox/daemon/internal/useCase/eventManager"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	signalUseCase "github.com/cybericebox/daemon/internal/useCase/signal"
)

// IRepository is the narrow data port: the three aggregate repos' query slices
// (each now including its list/count shapes), so the use case holds no postgres.*
// types. The gomock Querier and the real Queries both satisfy it structurally.
type IRepository interface {
	userRepo.Queries
	eventRepo.Queries
	eventConfigRepo.Queries
	liveScreenRepo.Queries
	eventManagerRepo.Queries
	participantRepo.Queries
	eventTeamRepo.Queries
	eventExerciseRepo.Queries
	eventFormRepo.Queries
	eventAnswerFileRepo.Queries
	eventChallengeRepo.Queries
	emailTemplateRepo.Queries
	inAppTemplateRepo.Queries
	eventNotificationRepo.Queries
	eventContentRepo.Queries
	eventChallengeGroupRepo.Queries
	challengeAttemptRepo.Queries
	requestIdempotencyRepo.Queries
	eventResultRepo.Queries
	scoreboardRepo.Queries
	labBindingRepo.Queries
	labAccessSyncRepo.Queries
	eventLabObservationRepo.Queries
	eventStandRepo.Queries
	teamChallengeRepo.Queries
	exerciseRepo.Queries
	signalOutboxRepo.CreateQueries
	mailRepo.Queries
	eventActivityRepo.Queries
}

// SignalPublisher is deliberately limited to emitting a prepared typed signal;
// event use cases never enqueue notification jobs directly.
type SignalPublisher interface {
	Publish(context.Context, signalModel.Type, signalModel.Payload) error
}

// SignalPublisherFactory binds a publisher to the active transaction's narrow
// repository. The production factory writes to signal_outbox in that same tx.
type SignalPublisherFactory func(IRepository) SignalPublisher

type InvitationNotifier interface {
	Notify(context.Context, uuid.UUID, notificationTypes.NotificationPayload, ...dispatchModel.NotifyOption) error
}

func NewOutboxSignalPublisherFactory(now func() time.Time) SignalPublisherFactory {
	return func(repo IRepository) SignalPublisher {
		return signalUseCase.NewPublisher(signalOutboxRepo.New(repo), now)
	}
}

// RequireManageEvent authorizes the authenticated user for the explicit event
// ID supplied by a management route. Origin and host never participate.
func (u *EventUseCase) RequireManageEvent(ctx context.Context, eventID, userID uuid.UUID) error {
	return eventManagerUseCase.NewAccessUseCase(u.managers).RequireManage(ctx, eventID, userID)
}

func (u *EventUseCase) RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error {
	return eventManagerUseCase.NewAccessUseCase(u.managers).RequireRead(ctx, eventID, userID)
}

type EventUseCase struct {
	observations      *eventLabObservationRepo.Repository
	standDeployBudget int
	prewarmLead       time.Duration
	prewarm           *prewarmState
	events            *eventRepo.Repository
	configs           *eventConfigRepo.Repository
	participants      *participantRepo.Repository
	userProfiles      interface {
		GetUserByID(context.Context, uuid.UUID) (postgres.User, error)
	}
	users                    *userRepo.Repository
	managers                 *eventManagerRepo.Repository
	teams                    *eventTeamRepo.Repository
	eventExercises           *eventExerciseRepo.Repository
	forms                    *eventFormRepo.Repository
	answerFiles              *eventAnswerFileRepo.Repository
	eventChallenges          *eventChallengeRepo.Repository
	content                  *eventContentRepo.Repository
	liveScreens              *liveScreenRepo.Repository
	teamChallenges           *teamChallengeRepo.Repository
	attempts                 *challengeAttemptRepo.Repository
	idempotency              *requestIdempotencyRepo.Repository
	results                  *eventResultRepo.Repository
	scoreboard               *scoreboardRepo.Repository
	labBindings              *labBindingRepo.Repository
	labAccessSyncs           *labAccessSyncRepo.Repository
	labObservations          *eventLabObservationRepo.Repository
	stands                   *eventStandRepo.Repository
	infra                    Infrastructure
	infrastructureCapability InfrastructureCapability
	topologies               TopologyResolver
	vpn                      VPNStore
	challengeGroups          *eventChallengeGroupRepo.Repository
	notificationDefaults     *eventNotificationRepo.Repository
	emailTemplates           *emailTemplateRepo.Repository
	emailImages              *emailUseCase.TemplateImages
	emailFooters             emailUseCase.FooterSource
	brandMedia               BrandMedia
	publicAPIBaseURL         string
	eventDomain              string
	idHost                   string
	setupTokens              interface {
		GenerateSetupToken(uuid.UUID) (string, error)
	}
	labSessions        LabSessionIssuer
	invitationNotifier InvitationNotifier
	// standInbox closes failed-laboratory requests on re-creation; nil until wired.
	standInbox       StandInbox
	inAppTemplates   *inAppTemplateRepo.Repository
	exercises        *exerciseRepo.Repository
	uow              postgres.IUnitOfWorker[IRepository]
	signalPublishers SignalPublisherFactory
	flagRandomBytes  int
	mail             *mailRepo.Repository
	// resultsCache shares results reads between the viewers of an event.
	resultsCache *resultsCache
	// activity is the event activity log (analytics); taskOpens dedupes the
	// task open beacon in process.
	activity  ActivityLog
	taskOpens *taskOpenThrottle
}

type Dependencies struct {
	Repo                     IRepository
	UoW                      postgres.IUnitOfWorker[IRepository]
	Infra                    Infrastructure
	InfrastructureCapability InfrastructureCapability
	Topologies               TopologyResolver
	VPN                      VPNStore
	SignalPublishers         SignalPublisherFactory
	FlagRandomBytes          int
	// StandDeployBudget is the Lab deploy calls per event and engine pass; 0 means the default.
	StandDeployBudget int
	// StandPrewarmLead is how long before an event's stand deploy time its images are prewarmed in
	// the image cache; 0 turns prewarming off.
	StandPrewarmLead time.Duration
	// Media backs Event email template images (validation and references).
	Media            emailUseCase.TemplateMedia
	BrandMedia       BrandMedia
	PublicAPIBaseURL string
	EventDomain      string
	// IDHost is ID_HOST, the sign-in app host of invitation links.
	IDHost      string
	SetupTokens interface {
		GenerateSetupToken(uuid.UUID) (string, error)
	}
	// LabSessions signs the proxy tokens of the web side of lab access; nil
	// when unconfigured.
	LabSessions LabSessionIssuer
}

// BrandMedia owns the event logo reference independently of email template images.
type BrandMedia interface {
	UploadFile(context.Context, string, string, io.Reader, uuid.UUID) (mediaModel.File, error)
	GetFile(context.Context, uuid.UUID) (mediaModel.File, error)
	StreamFile(context.Context, uuid.UUID) (io.ReadCloser, mediaModel.File, error)
	GetReferences(context.Context, string, uuid.UUID) ([]uuid.UUID, error)
	AddReference(context.Context, string, uuid.UUID, uuid.UUID) error
	ReplaceReferences(context.Context, string, uuid.UUID, []uuid.UUID) error
}

type Infrastructure interface {
	DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error
	LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error)
	EnsureVPNGroup(context.Context, string) error
	EnsureLabClient(context.Context, string, string) (string, error)
	DestroyLabGroup(context.Context, string) error
}

// InfrastructureCapability is intentionally narrower than the command port:
// event planning only asks whether Laboratories are usable right now.
type InfrastructureCapability interface {
	RequireLaboratories(context.Context) error
}

func infraUnavailable() error { return infraModel.ErrInfrastructureUnavailable.Err() }

type VPNStore interface {
	StoreConfig(context.Context, uuid.UUID, vpnModel.Scope, uuid.NullUUID, string) error
	GetConfig(context.Context, uuid.UUID, vpnModel.Scope, uuid.NullUUID) (string, error)
}

// TopologyResolver supplies a pinned variant's deployable topology. Its
// implementation remains in the exercise use case, which owns encryption.
type TopologyResolver interface {
	ResolveDeployedTopology(context.Context, uuid.UUID, int32) (exerciseModel.Topology, error)
}

type VersionTopologyResolver interface {
	ResolveVersionTopologies(context.Context, uuid.UUID) ([]exerciseModel.Topology, error)
}

func NewEventUseCase(deps Dependencies) *EventUseCase {
	flagRandomBytes := deps.FlagRandomBytes
	if flagRandomBytes == 0 {
		flagRandomBytes = 20
	}
	standDeployBudget := deps.StandDeployBudget
	if standDeployBudget <= 0 {
		standDeployBudget = DefaultStandDeployBudget
	}
	return &EventUseCase{
		standDeployBudget:        standDeployBudget,
		prewarmLead:              deps.StandPrewarmLead,
		prewarm:                  newPrewarmState(),
		events:                   eventRepo.New(deps.Repo),
		configs:                  eventConfigRepo.New(deps.Repo),
		participants:             participantRepo.New(deps.Repo),
		userProfiles:             deps.Repo,
		users:                    userRepo.New(deps.Repo),
		managers:                 eventManagerRepo.New(deps.Repo),
		teams:                    eventTeamRepo.New(deps.Repo),
		eventExercises:           eventExerciseRepo.New(deps.Repo),
		forms:                    eventFormRepo.New(deps.Repo),
		answerFiles:              eventAnswerFileRepo.New(deps.Repo),
		eventChallenges:          eventChallengeRepo.New(deps.Repo),
		content:                  eventContentRepo.New(deps.Repo),
		liveScreens:              liveScreenRepo.New(deps.Repo),
		teamChallenges:           teamChallengeRepo.New(deps.Repo),
		attempts:                 challengeAttemptRepo.New(deps.Repo),
		idempotency:              requestIdempotencyRepo.New(deps.Repo),
		results:                  eventResultRepo.New(deps.Repo),
		scoreboard:               scoreboardRepo.New(deps.Repo),
		labBindings:              labBindingRepo.New(deps.Repo),
		observations:             eventLabObservationRepo.New(deps.Repo),
		labAccessSyncs:           labAccessSyncRepo.New(deps.Repo),
		labObservations:          eventLabObservationRepo.New(deps.Repo),
		stands:                   eventStandRepo.New(deps.Repo),
		infra:                    deps.Infra,
		infrastructureCapability: deps.InfrastructureCapability,
		topologies:               deps.Topologies,
		vpn:                      deps.VPN,
		challengeGroups:          eventChallengeGroupRepo.New(deps.Repo),
		notificationDefaults:     eventNotificationRepo.New(deps.Repo),
		emailTemplates:           emailTemplateRepo.New(deps.Repo),
		emailImages:              emailUseCase.NewTemplateImages(deps.Media, emailTemplateRepo.New(deps.Repo)),
		brandMedia:               deps.BrandMedia,
		publicAPIBaseURL:         deps.PublicAPIBaseURL,
		eventDomain:              deps.EventDomain,
		idHost:                   deps.IDHost,
		setupTokens:              deps.SetupTokens,
		labSessions:              deps.LabSessions,
		inAppTemplates:           inAppTemplateRepo.New(deps.Repo),
		exercises:                exerciseRepo.New(deps.Repo),
		flagRandomBytes:          flagRandomBytes,
		resultsCache:             newResultsCache(),
		uow:                      deps.UoW,
		signalPublishers:         deps.SignalPublishers,
		mail:                     mailRepo.New(deps.Repo),
		activity:                 eventActivityRepo.New(deps.Repo),
		taskOpens:                newTaskOpenThrottle(eventActivityModel.TaskOpenWindow),
	}
}

// StandInbox closes the failed-laboratory inbox request of a team for every
// recipient (satisfied by inbox.RequestRouter).
type StandInbox interface {
	StandRecreated(ctx context.Context, eventID, teamID, by uuid.UUID) error
}

// SetStandInbox wires the stand inbox after the dispatcher exists.
func (u *EventUseCase) SetStandInbox(inbox StandInbox) {
	u.standInbox = inbox
}

func (u *EventUseCase) SetInvitationNotifier(notifier InvitationNotifier) {
	u.invitationNotifier = notifier
}

// SetEmailFooters wires the mail use case's footer builder for template
// previews (it is built after the Event use case).
func (u *EventUseCase) SetEmailFooters(footers emailUseCase.FooterSource) {
	u.emailFooters = footers
}

// mutateEvent: fetch → domain mutation → whole-identity write under the
// optimistic lock; zero rows re-reads to tell 404 from 409 (mutateExercise twin).
func (u *EventUseCase) mutateEvent(ctx context.Context, id uuid.UUID, mutate func(*eventModel.Event) error) (eventModel.Event, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.Event{}, eventModel.ErrEventNotFound.Err()
		}
		return eventModel.Event{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	expectedUpdatedAt := e.UpdatedAt
	if err = mutate(&e); err != nil {
		return eventModel.Event{}, err
	}
	affected, err := u.events.Update(ctx, e, expectedUpdatedAt)
	if err != nil {
		return eventModel.Event{}, classifyEventWriteError(err, "update")
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return eventModel.Event{}, eventModel.ErrEventNotFound.Err()
		}
		return eventModel.Event{}, eventModel.ErrEventModified.Err()
	}
	return e, nil
}

// classifyEventWriteError maps tag-window collisions to the
// domain 409; anything else becomes a platform error. The single call path
// for BOTH create and update.
func classifyEventWriteError(err error, action string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ExclusionViolation && pgErr.ConstraintName == "events_tag_window_no_overlap" {
		return eventModel.ErrEventExists.Err()
	}
	if creator, ok := repositoryTools.UniqueViolationError(err, eventModel.ErrEventExists); ok {
		return creator.Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to " + action + " event").Err()
}

// mutateEventConfig: fetch → domain mutation → whole-config write under the
// optimistic lock; zero rows re-reads to tell 404 from 409 (mutateEvent twin).
// A missing initial fetch here (ErrEventConfigNotFound) is expected to be
// rare in practice: CreateEvent creates the config row alongside the event,
// and GetEventConfig self-heals a missing one on read — this path only fires
// if UpdateEventConfig is the very first call for an event whose config
// insert hasn't landed (or raced) yet.
func (u *EventUseCase) mutateEventConfig(ctx context.Context, eventID uuid.UUID, mutate func(*eventConfigModel.EventConfig) error) (eventConfigModel.EventConfig, error) {
	c, err := u.configs.Get(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventConfigModel.EventConfig{}, eventConfigModel.ErrEventConfigNotFound.Err()
		}
		return eventConfigModel.EventConfig{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	expected := c.UpdatedAt
	if err = mutate(&c); err != nil {
		return eventConfigModel.EventConfig{}, err
	}
	affected, err := u.configs.Update(ctx, c, expected)
	if err != nil {
		return eventConfigModel.EventConfig{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event config").Err()
	}
	if affected == 0 {
		if _, err = u.configs.Get(ctx, eventID); err != nil {
			return eventConfigModel.EventConfig{}, eventConfigModel.ErrEventConfigNotFound.Err()
		}
		return eventConfigModel.EventConfig{}, eventModel.ErrEventModified.Err()
	}
	return c, nil
}
