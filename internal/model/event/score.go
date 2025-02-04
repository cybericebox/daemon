package eventModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
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
)

var (
	ErrEventScore = err.ErrInternal.WithObjectCode(model.EventScoreObjectCode)

	ErrEventScoreScoreNotAvailable = err.ErrForbidden.WithObjectCode(model.EventScoreObjectCode).WithMessage("Score not available").WithDetailCode(1) // 61701
)
