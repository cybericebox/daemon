package eventModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	Event struct {
		ID uuid.UUID `validate:"omitempty,uuid"`

		Type          int32 `validate:"required,number,oneof=0 1"`
		Availability  int32 `validate:"required,number,oneof=0 1"`
		Participation int32 `validate:"required,number,oneof=0 1"`

		Tag         string `validate:"required,min=3,max=20,lowercase,alphanum"`
		Name        string `validate:"required,min=3,max=50,alphanum"`
		Description string `validate:"required,min=1"`
		Rules       string `validate:"required,min=1"`
		Picture     string `validate:"omitempty,uuid|url"`

		DynamicScoring        bool  `validate:"omitempty,boolean"`
		DynamicMaxScore       int32 `validate:"required_with=DynamicScoring,min=2,max=100,gtfield=DynamicMinScore"`
		DynamicMinScore       int32 `validate:"required_with=DynamicScoring,min=1,max=99,ltfield=DynamicMaxScore"`
		DynamicSolveThreshold int32 `validate:"required_with=DynamicScoring,min=1,max=1000"`

		Registration           int32 `validate:"required,number,oneof=0 1 2"`
		ScoreboardAvailability int32 `validate:"required,number,oneof=0 1 2"`
		ParticipantsVisibility int32 `validate:"required,number,oneof=0 1 2"`

		PublishTime  time.Time `validate:"required"`
		StartTime    time.Time `validate:"required,gtefield=PublishTime"`
		FinishTime   time.Time `validate:"required,gtfield=StartTime"`
		WithdrawTime time.Time `validate:"required,gtefield=FinishTime"`

		CreatedAt time.Time
		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		ChallengesCount int64
		TeamsCount      int64
	}

	// EventInfo is a struct that contains all the information about an event for response
	EventInfo struct {
		Type          int32
		Participation int32

		Tag         string
		Name        string
		Description string
		Rules       string
		Picture     string

		Registration           int32
		ScoreboardAvailability int32
		ParticipantsVisibility int32

		StartTime  time.Time
		FinishTime time.Time
	}

	EventMetadata struct {
		Description string
		Rules       string
		Picture     string
	}
)

var (
	ErrEvent = err.ErrInternal.WithObjectCode(model.EventObjectCode)

	ErrEventEventDataStale = err.ErrConflict.WithObjectCode(model.EventObjectCode).WithMessage("Event data is stale").WithDetailCode(1) // 71301

	ErrEventEventNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventObjectCode).WithMessage("Event not found").WithDetailCode(1) // 31301

	ErrEventEventExists = err.ErrObjectExists.WithObjectCode(model.EventObjectCode).WithMessage("Event already exists").WithDetailCode(1) // 41301

	ErrEventRegistrationClosed = err.ErrForbidden.WithObjectCode(model.EventObjectCode).WithMessage("Event registration is closed").WithDetailCode(1) // 61301
	ErrEventEventNotJoined     = err.ErrForbidden.WithObjectCode(model.EventObjectCode).WithMessage("Event not joined").WithDetailCode(2)             // 61302
)

// Event running statuses
const (
	EventNotPublishedStatus = int32(iota)
	EventPublishedStatus
	EventStartedStatus
	EventFinishedStatus
	EventWithdrawnStatus
)

// Event types
const (
	CompetitionEventType = int32(iota)
	TrainingEventType
)

// Event registration types
const (
	ClosedRegistrationType = int32(iota)
	ApprovalRegistrationType
	OpenRegistrationType
)

// Event participation statuses
const (
	NoParticipationStatus = int32(iota)
	PendingParticipationStatus
	ApprovedParticipationStatus
	RejectedParticipationStatus
)

// Event participation types
const (
	IndividualParticipationType = int32(iota)
	TeamParticipationType
)

// Event availability types
const (
	PrivateAvailabilityType = int32(iota)
	PublicAvailabilityType
)

// Event scoreboard availability types
const (
	HiddenScoreboardAvailabilityType = int32(iota)
	PrivateScoreboardAvailabilityType
	PublicScoreboardAvailabilityType
)

// Event participants visibility types
const (
	HiddenParticipantsVisibilityType = int32(iota)
	PrivateParticipantsVisibilityType
	PublicParticipantsVisibilityType
)
