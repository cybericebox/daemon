package eventChallengeModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

var (
	ErrEventChallengeIdentityInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
						WithMessage("Event exercise and task identifiers are required").WithDetailCode(1)
	ErrEventChallengePointsInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Event challenge points must be positive").WithDetailCode(2)
	ErrEventChallengeNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Event challenge not found").WithDetailCode(3)
	ErrEventChallengeOrderInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Challenge order must contain every board challenge exactly once").WithDetailCode(4)
	ErrEventChallengeHintCostsInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
						WithMessage("Hint costs must name hints of this challenge and be 0-10000").WithDetailCode(31)
	ErrEventChallengeHintNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Hint not found").WithDetailCode(32)
	ErrEventChallengeHintsDisabled = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Hints are not enabled for this challenge").WithDetailCode(33)
	ErrEventChallengeBoardOrderInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
						WithMessage("Group order must contain every challenge of the group exactly once").WithDetailCode(35)
	ErrEventChallengePrerequisitesInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
						WithMessage("Challenge prerequisites must be distinct challenges from the same board revision").WithDetailCode(8)
)
