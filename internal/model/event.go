package model

import (
	"github.com/cybericebox/daemon/internal/appError"
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

		CreatedAt time.Time
	}

	Challenge struct {
		ID         uuid.UUID `validate:"omitempty,uuid"`
		EventID    uuid.UUID `validate:"required,uuid"`
		CategoryID uuid.UUID `validate:"required,uuid"`

		ExerciseID     uuid.UUID `validate:"required,uuid"`
		ExerciseTaskID uuid.UUID `validate:"required,uuid"`

		Name        string `validate:"required,min=3,max=50,alphanum"`
		Description string `validate:"required,min=1"`
		Points      int32  `validate:"required,min=1,max=1000"`

		Order int32 `validate:"required,number"`

		CreatedAt time.Time
	}

	Order struct {
		ID         uuid.UUID `validate:"required,uuid"`
		CategoryID uuid.UUID `validate:"omitempty,uuid"`
		OrderIndex int32     `validate:"required,number"`
	}

	Team struct {
		ID      uuid.UUID `validate:"omitempty,uuid"`
		EventID uuid.UUID `validate:"required,uuid"`

		Name     string `validate:"required,min=3,max=50,alphanum"`
		JoinCode string `validate:"-"`

		LaboratoryID uuid.NullUUID

		CreatedAt time.Time
	}

	TeamInfo struct {
		ID   uuid.UUID
		Name string
	}

	CategoryInfo struct {
		ID         uuid.UUID
		Name       string
		Challenges []*ChallengeInfo
	}

	ChallengeInfo struct {
		ID          uuid.UUID
		Name        string
		Description string
		Points      int32

		Solved bool
	}

	ChallengeSoledBy struct {
		ChallengeID uuid.UUID
		Teams       []*TeamSolvedChallenge
	}

	TeamSolvedChallenge struct {
		ID       uuid.UUID
		Name     string
		SolvedAt time.Time
	}

	EventScore struct {
		TeamsScores   []TeamScore
		ChallengeList []ChallengeInfo
	}

	TeamScore struct {
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
	ErrEventAlreadyJoined      = appError.NewError().WithCode(appError.CodeAlreadyExists.WithMessage("event already joined"))
	ErrEventNotJoined          = appError.NewError().WithCode(appError.CodeForbidden.WithMessage("event not joined"))
	ErrEventRegistrationClosed = appError.NewError().WithCode(appError.CodeForbidden.WithMessage("event registration is closed"))
	ErrScoreNotAvailable       = appError.NewError().WithCode(appError.CodeForbidden.WithMessage("score not available"))
	//
	ErrUserAlreadyInTeam    = appError.NewError().WithCode(appError.CodeAlreadyExists.WithMessage("user already in team"))
	ErrTeamWrongCredentials = appError.NewError().WithCode(appError.CodeUnauthorized.WithMessage("team wrong credentials"))
	//

	ErrSolutionAttemptNotAllowed = appError.NewError().WithCode(appError.CodeForbidden.WithMessage("solution attempt not allowed"))
)

// Event types
const (
	CompetitionEventType = int32(iota)
	TrainingEventType
)

// Event registration types
const (
	OpenRegistrationType = int32(iota)
	ApprovalRegistrationType
	ClosedRegistrationType
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
	PublicAvailabilityType = int32(iota)
	PrivateAvailabilityType
)

// Event scoreboard availability types
const (
	PublicScoreboardAvailabilityType = int32(iota)
	PrivateScoreboardAvailabilityType
	HiddenScoreboardAvailabilityType
)

// Event participants visibility types
const (
	PublicParticipantsVisibilityType = int32(iota)
	PrivateParticipantsVisibilityType
	NoneParticipantsVisibilityType
)
