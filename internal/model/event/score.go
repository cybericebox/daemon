package eventModel

import (
	"time"

	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
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
	ErrEventScoreNotAvailable = err.ErrForbidden.WithObjectCode(model.EventScoreObjectCode).WithMessage("Score not available").WithDetailCode(1) // 61701
)
