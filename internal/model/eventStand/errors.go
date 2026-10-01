package eventStandModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// EventStandObjectCode — next free detail code: 10
var (
	ErrStandInfrastructureNotAllowed = err.ErrConflict.WithObjectCode(model.EventStandObjectCode).
						WithMessage("Infrastructure challenges are not allowed for this event").WithDetailCode(1)
	ErrStandSettingsInvalid = err.ErrInvalidData.WithObjectCode(model.EventStandObjectCode).
				WithMessage("Laboratory deploy lead must be 5-1440 minutes and teardown delay 0-10080 minutes").WithDetailCode(2)
	ErrStandTeamNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventStandObjectCode).
				WithMessage("Laboratory team not found").WithDetailCode(3)
	ErrStandNotDeployed = err.ErrConflict.WithObjectCode(model.EventStandObjectCode).
				WithMessage("The team laboratory is not deployed").WithDetailCode(4)
	ErrStandEventFinished = err.ErrConflict.WithObjectCode(model.EventStandObjectCode).
				WithMessage("Laboratories cannot be recreated after the event finish").WithDetailCode(5)
	ErrStandModeratorsTeamUnavailable = err.ErrConflict.WithObjectCode(model.EventStandObjectCode).
						WithMessage("The moderators team is unavailable for this event").WithDetailCode(6)
	ErrStandLabNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventStandObjectCode).
				WithMessage("The challenge has no laboratory").WithDetailCode(7)
	ErrStandLabClientMissing = err.ErrConflict.WithObjectCode(model.EventStandObjectCode).
					WithMessage("The participant has no laboratory client yet").WithDetailCode(8)
	ErrStandLabNoWebDevice = err.ErrObjectNotFound.WithObjectCode(model.EventStandObjectCode).
				WithMessage("The device has no web address").WithDetailCode(9)
)
