package teamChallengeModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

var ErrTeamChallengeInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Team challenge materialization data is invalid").WithDetailCode(9)

var ErrTeamChallengeTransition = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Team challenge readiness transition is invalid").WithDetailCode(10)

var ErrTeamChallengePrerequisites = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Challenge prerequisites are not solved").WithDetailCode(12)
