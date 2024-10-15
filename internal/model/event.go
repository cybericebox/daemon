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

		Data ChallengeData `validate:"required"`

		ExerciseID     uuid.UUID `validate:"required,uuid"`
		ExerciseTaskID uuid.UUID `validate:"required,uuid"`

		Order int32 `validate:"required,number"`

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

		LaboratoryID uuid.NullUUID

		CreatedAt time.Time
	}

	TeamInfo struct {
		ID   uuid.UUID
		Name string
	}

	Participant struct {
		UserID  uuid.UUID     `validate:"required,uuid"`
		EventID uuid.UUID     `validate:"required,uuid"`
		TeamID  uuid.NullUUID `validate:"omitempty,uuid"`

		Name           string `validate:"required,min=3,max=255,alphanum"`
		ApprovalStatus int32  `validate:"required,number,oneof=0 1 2"`

		CreatedAt time.Time
	}

	ParticipantInfo struct {
		UserID  uuid.UUID     `validate:"required,uuid"`
		EventID uuid.UUID     `validate:"required,uuid"`
		TeamID  uuid.NullUUID `validate:"omitempty,uuid"`
		Name    string        `validate:"required,min=3,max=255,alphanum"`
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

	TeamsChallengeSolvedBy struct {
		ChallengeID uuid.UUID
		Teams       []*TeamChallengeSolvedBy
	}

	TeamChallengeSolvedBy struct {
		ID       uuid.UUID
		Name     string
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
	ErrEvent = appError.ErrInternal.WithObjectCode(eventObjectCode)

	ErrEventEventNotFound = appError.ErrObjectNotFound.WithObjectCode(eventObjectCode).WithMessage("Event not found")

	ErrEventEventExists   = appError.ErrObjectExists.WithObjectCode(eventObjectCode).WithMessage("Event already exists").WithDetailCode(1)
	ErrEventAlreadyJoined = appError.ErrObjectExists.WithObjectCode(eventObjectCode).WithMessage("Event already joined").WithDetailCode(2)

	ErrEventRegistrationClosed = appError.ErrForbidden.WithObjectCode(eventObjectCode).WithMessage("Event registration is closed").WithDetailCode(1)
	ErrEventEventNotJoined     = appError.ErrForbidden.WithObjectCode(eventObjectCode).WithMessage("Event not joined").WithDetailCode(2)

	ErrEventParticipant = appError.ErrInternal.WithObjectCode(eventParticipantObjectCode)

	ErrEventParticipantExists = appError.ErrObjectExists.WithObjectCode(eventParticipantObjectCode).WithMessage("Participant already exists")

	ErrEventParticipantNotFound     = appError.ErrObjectNotFound.WithObjectCode(eventParticipantObjectCode).WithMessage("Participant not found").WithDetailCode(1)
	ErrEventParticipantTeamNotFound = appError.ErrObjectNotFound.WithObjectCode(eventParticipantObjectCode).WithMessage("Participant team not found").WithDetailCode(2)

	ErrEventChallengeCategory = appError.ErrInternal.WithObjectCode(eventChallengeCategoryObjectCode)

	ErrEventChallengeCategoryCategoryExists = appError.ErrObjectExists.WithObjectCode(eventChallengeCategoryObjectCode).WithMessage("Event challenge category already exists")

	ErrEventChallengeCategoryCategoryNotFound = appError.ErrObjectNotFound.WithObjectCode(eventChallengeCategoryObjectCode).WithMessage("Event challenge category not found")

	ErrEventChallenge = appError.ErrInternal.WithObjectCode(eventChallengeObjectCode)

	ErrEventChallengeChallengeExists = appError.ErrObjectExists.WithObjectCode(eventChallengeObjectCode).WithMessage("Event challenge already exists")

	ErrEventChallengeChallengeNotFound = appError.ErrObjectNotFound.WithObjectCode(eventChallengeObjectCode).WithMessage("Event challenge not found")

	ErrEventScore = appError.ErrInternal.WithObjectCode(eventScoreObjectCode)

	ErrEventScoreScoreNotAvailable = appError.ErrForbidden.WithObjectCode(eventScoreObjectCode).WithMessage("Score not available")

	ErrEventTeam = appError.ErrInternal.WithObjectCode(eventTeamObjectCode)

	ErrEventTeamTeamExists        = appError.ErrObjectExists.WithObjectCode(eventTeamObjectCode).WithMessage("Team already exists").WithDetailCode(1)
	ErrEventTeamUserAlreadyInTeam = appError.ErrObjectExists.WithObjectCode(eventTeamObjectCode).WithMessage("User already in team").WithDetailCode(2)

	ErrEventTeamTeamNotFound = appError.ErrObjectNotFound.WithObjectCode(eventTeamObjectCode).WithMessage("Team not found")

	ErrEventTeamWrongCredentials = appError.ErrInvalidData.WithObjectCode(eventTeamObjectCode).WithMessage("Team wrong credentials")

	ErrEventTeamChallenge = appError.ErrInternal.WithObjectCode(eventTeamChallengeObjectCode)

	ErrEventTeamChallengeSolutionAttemptNotAllowed = appError.ErrForbidden.WithObjectCode(eventTeamChallengeObjectCode).WithMessage("Solution attempt not allowed")
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
