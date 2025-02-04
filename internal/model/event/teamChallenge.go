package eventModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	TeamChallenge struct {
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

	SolutionForTimeline struct {
		Date   time.Time
		Points int
	}
)

var (
	ErrEventTeamChallenge = err.ErrInternal.WithObjectCode(model.EventTeamChallengeObjectCode)

	ErrEventTeamChallengeSolutionAttemptNotAllowed = err.ErrForbidden.WithObjectCode(model.EventTeamChallengeObjectCode).WithMessage("Solution attempt not allowed").WithDetailCode(1) // 61901

	ErrEventTeamChallengeAlreadySolved = err.ErrConflict.WithObjectCode(model.EventTeamChallengeObjectCode).WithMessage("Challenge already solved").WithDetailCode(1) // 71901
)
