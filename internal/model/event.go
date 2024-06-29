package model

import (
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"net/http"
	"time"
)

type (
	Event struct {
		ID uuid.UUID

		Type          int32
		Availability  int32
		Participation int32

		Tag         string
		Name        string
		Description string
		Rules       string
		Picture     string

		DynamicScoring        bool
		DynamicMaxScore       int32
		DynamicMinScore       int32
		DynamicSolveThreshold int32

		Registration           int32
		ScoreboardAvailability int32
		ParticipantsVisibility int32

		PublishTime  time.Time
		StartTime    time.Time
		FinishTime   time.Time
		WithdrawTime time.Time

		CreatedAt time.Time

		ChallengesCount int64
		TeamsCount      int64
	}

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
		ID      uuid.UUID
		EventID uuid.UUID

		Name  string
		Order int32

		CreatedAt time.Time
	}

	Challenge struct {
		ID         uuid.UUID
		EventID    uuid.UUID
		CategoryID uuid.UUID

		ExerciseID     uuid.UUID
		ExerciseTaskID uuid.UUID

		Name        string
		Description string
		Points      int32

		Order int32

		CreatedAt time.Time
	}

	Order struct {
		ID         uuid.UUID
		CategoryID uuid.UUID
		Index      int32
	}

	Team struct {
		ID      uuid.UUID
		EventID uuid.UUID

		Name     string
		JoinCode string

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
	ErrEventAlreadyJoined      = tools.NewError("event already joined", http.StatusConflict)
	ErrEventRegistrationClosed = tools.NewError("event registration is closed", http.StatusForbidden)
	ErrEventNotJoined          = tools.NewError("event not joined", http.StatusForbidden)
	ErrScoreNotAvailable       = tools.NewError("score not available", http.StatusForbidden)

	ErrTeamExists           = tools.NewError("team exists", http.StatusConflict)
	ErrUserAlreadyInTeam    = tools.NewError("user already in team", http.StatusConflict)
	ErrTeamWrongCredentials = tools.NewError("team wrong credentials", http.StatusUnauthorized)
	ErrTeamNotFound         = tools.NewError("team not found", http.StatusNotFound)
	ErrLaboratoryNotFound   = tools.NewError("laboratory not found", http.StatusNotFound)

	ErrSolutionAttemptNotAllowed = tools.NewError("solution attempt not allowed", http.StatusForbidden)
	ErrIncorrectSolution         = tools.NewError("incorrect solution", http.StatusBadRequest)
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
