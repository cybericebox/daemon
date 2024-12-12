package model

import (
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

	ChallengeCategory struct {
		ID      uuid.UUID `validate:"omitempty,uuid"`
		EventID uuid.UUID `validate:"required,uuid"`

		Name  string `validate:"required,min=3,max=50,alphanum"`
		Order int32  `validate:"required,number"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	Challenge struct {
		ID         uuid.UUID `validate:"omitempty,uuid"`
		EventID    uuid.UUID `validate:"required,uuid"`
		CategoryID uuid.UUID `validate:"required,uuid"`

		Data ChallengeData `validate:"required"`

		ExerciseID     uuid.UUID `validate:"required,uuid"`
		ExerciseTaskID uuid.UUID `validate:"required,uuid"`

		Order int32 `validate:"required,number"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	ChallengeData struct {
		Name          string         `validate:"required,min=3,max=50,alphanum"`
		Description   string         `validate:"required,min=1"`
		Points        int32          `validate:"required,min=1,max=1000"`
		AttachedFiles []ExerciseFile `validate:"omitempty,dive"`
	}

	Order struct {
		ID         uuid.UUID `validate:"required,uuid"`
		CategoryID uuid.UUID `validate:"omitempty,uuid"`
		Index      int32     `validate:"required,number"`
	}

	Team struct {
		ID      uuid.UUID `validate:"omitempty,uuid"`
		EventID uuid.UUID `validate:"required,uuid"`

		Name     string `validate:"required,min=3,max=50,alphanum"`
		JoinCode string `validate:"-"`

		Hidden bool `validate:"required,boolean"`

		ParticipantsCount int64

		LaboratoryID uuid.NullUUID

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	TeamInfo struct {
		ID   uuid.UUID
		Name string
	}

	Participant struct {
		UserID   uuid.UUID     `validate:"required,uuid"`
		EventID  uuid.UUID     `validate:"required,uuid"`
		TeamID   uuid.NullUUID `validate:"omitempty,uuid"`
		TeamName string        `validate:"omitempty,min=3,max=50,alphanum"`

		Name  string `validate:"required,min=3,max=255,alphanum"`
		Email string `validate:"required,email"`

		ApprovalStatus int32 `validate:"required,number,oneof=0 1 2"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	ParticipantInfo struct {
		UserID  uuid.UUID     `validate:"required,uuid"`
		EventID uuid.UUID     `validate:"required,uuid"`
		TeamID  uuid.NullUUID `validate:"omitempty,uuid"`
		Name    string        `validate:"required,min=3,max=255,alphanum"`
		Email   string        `validate:"required,email"`
	}

	CategoryInfo struct {
		ID         uuid.UUID
		Name       string
		Challenges []*ChallengeInfo
	}

	ChallengeInfo struct {
		ID            uuid.UUID
		Name          string
		Description   string
		Points        int32
		AttachedFiles []ExerciseFile

		Solved bool
	}

	TeamChallenge struct {
		EventID     uuid.UUID
		TeamID      uuid.UUID
		ChallengeID uuid.UUID
		Flag        string
	}

	TeamChallengeSolutionAttempt struct {
		ID              uuid.UUID
		EventID         uuid.UUID
		ChallengeID     uuid.UUID
		TeamID          uuid.UUID
		TeamName        string
		ParticipantID   uuid.UUID
		ParticipantName string
		Answer          string
		Flag            string
		IsCorrect       bool
		Timestamp       time.Time
	}

	TeamsChallengeSolvedBy struct {
		ChallengeID uuid.UUID
		Teams       []*TeamChallengeSolvedBy
	}

	TeamChallengeSolvedBy struct {
		ID       uuid.UUID
		Name     string
		Hidden   bool `json:"-"`
		SolvedAt time.Time
	}

	EventScore struct {
		TeamsScores []TeamScore
		Challenges  []ChallengeInfo
	}

	TeamScore struct {
		TeamID            uuid.UUID
		Rank              int
		TeamName          string
		Score             int
		TeamSolutions     map[uuid.UUID]TeamSolution
		LatestSolution    time.Time
		TeamScoreTimeline [][]interface{}
	}

	TeamSolution struct {
		ID   uuid.UUID
		Rank int
	}

	SolutionForTimeline struct {
		Date   time.Time
		Points int
	}
)

var (
	// Event
	ErrEvent = err.ErrInternal.WithObjectCode(eventObjectCode)

	ErrEventEventDataStale = err.ErrConflict.WithObjectCode(eventObjectCode).WithMessage("Event data is stale").WithDetailCode(1) // 71301

	ErrEventEventNotFound = err.ErrObjectNotFound.WithObjectCode(eventObjectCode).WithMessage("Event not found").WithDetailCode(1) // 31301

	ErrEventEventExists = err.ErrObjectExists.WithObjectCode(eventObjectCode).WithMessage("Event already exists").WithDetailCode(1) // 41301

	ErrEventRegistrationClosed = err.ErrForbidden.WithObjectCode(eventObjectCode).WithMessage("Event registration is closed").WithDetailCode(1) // 61301
	ErrEventEventNotJoined     = err.ErrForbidden.WithObjectCode(eventObjectCode).WithMessage("Event not joined").WithDetailCode(2)             // 61302

	// Event participant
	ErrEventParticipant = err.ErrInternal.WithObjectCode(eventParticipantObjectCode)

	ErrEventParticipantExists = err.ErrObjectExists.WithObjectCode(eventParticipantObjectCode).WithMessage("Participant already exists").WithDetailCode(1) // 41601

	ErrEventParticipantNotFound     = err.ErrObjectNotFound.WithObjectCode(eventParticipantObjectCode).WithMessage("Participant not found").WithDetailCode(1)      // 31601
	ErrEventParticipantTeamNotFound = err.ErrObjectNotFound.WithObjectCode(eventParticipantObjectCode).WithMessage("Participant team not found").WithDetailCode(2) // 31602

	// Event challenge category
	ErrEventChallengeCategory = err.ErrInternal.WithObjectCode(eventChallengeCategoryObjectCode)

	ErrEventChallengeCategoryCategoryExists = err.ErrObjectExists.WithObjectCode(eventChallengeCategoryObjectCode).WithMessage("Event challenge category already exists").WithDetailCode(1) // 41501

	ErrEventChallengeCategoryCategoryNotFound = err.ErrObjectNotFound.WithObjectCode(eventChallengeCategoryObjectCode).WithMessage("Event challenge category not found").WithDetailCode(1) // 31501

	ErrEventChallengeCategoryCategoryHasChallenges = err.ErrConflict.WithObjectCode(eventChallengeCategoryObjectCode).WithMessage("Event challenge category has challenges").WithDetailCode(1) // 71501
	ErrEventChallengeCategoryCategoryDataStale     = err.ErrConflict.WithObjectCode(eventChallengeCategoryObjectCode).WithMessage("Event challenge category data is stale").WithDetailCode(2)  // 71502

	// Event challenge
	ErrEventChallenge = err.ErrInternal.WithObjectCode(eventChallengeObjectCode)

	ErrEventChallengeChallengeExists = err.ErrObjectExists.WithObjectCode(eventChallengeObjectCode).WithMessage("Event challenge already exists").WithDetailCode(1) // 41401

	ErrEventChallengeChallengeNotFound = err.ErrObjectNotFound.WithObjectCode(eventChallengeObjectCode).WithMessage("Event challenge not found").WithDetailCode(1) // 31401

	// Event score
	ErrEventScore = err.ErrInternal.WithObjectCode(eventScoreObjectCode)

	ErrEventScoreScoreNotAvailable = err.ErrForbidden.WithObjectCode(eventScoreObjectCode).WithMessage("Score not available").WithDetailCode(1) // 61701

	// Event team
	ErrEventTeam = err.ErrInternal.WithObjectCode(eventTeamObjectCode)

	ErrEventTeamTeamExists        = err.ErrObjectExists.WithObjectCode(eventTeamObjectCode).WithMessage("Team already exists").WithDetailCode(1)  // 41801
	ErrEventTeamUserAlreadyInTeam = err.ErrObjectExists.WithObjectCode(eventTeamObjectCode).WithMessage("User already in team").WithDetailCode(2) // 41802

	ErrEventTeamTeamNotFound = err.ErrObjectNotFound.WithObjectCode(eventTeamObjectCode).WithMessage("Team not found").WithDetailCode(1) // 31801

	ErrEventTeamWrongCredentials = err.ErrInvalidData.WithObjectCode(eventTeamObjectCode).WithMessage("Team wrong credentials").WithDetailCode(1) // 21801

	// Event team challenge
	ErrEventTeamChallenge = err.ErrInternal.WithObjectCode(eventTeamChallengeObjectCode)

	ErrEventTeamChallengeSolutionAttemptNotAllowed = err.ErrForbidden.WithObjectCode(eventTeamChallengeObjectCode).WithMessage("Solution attempt not allowed").WithDetailCode(1) // 61901

	ErrEventTeamChallengeAlreadySolved = err.ErrConflict.WithObjectCode(eventTeamChallengeObjectCode).WithMessage("Challenge already solved").WithDetailCode(1) // 71901
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
