package eventChallengeGroup

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

var (
	ErrChallengeGroupInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Challenge group name or order is invalid").WithDetailCode(5)
	ErrChallengeGroupExists = err.ErrObjectExists.WithObjectCode(model.EventChallengeObjectCode).
				WithMessage("Challenge group name or order already exists in this event").WithDetailCode(6)
	ErrChallengeGroupNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Challenge group not found").WithDetailCode(7)
	ErrChallengeGroupUpdateConflict = err.ErrObjectExists.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Another challenge group already uses this name or order").WithDetailCode(27)
	ErrChallengeGroupOrderInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
					WithMessage("Group order must contain every challenge group exactly once").WithDetailCode(28)
)
