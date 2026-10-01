package eventExerciseModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

var (
	ErrEventExerciseIdentityInvalid = err.ErrInvalidData.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("Event, exercise, and version identifiers are required").WithDetailCode(1)
	ErrEventExerciseVariantModeInvalid = err.ErrInvalidData.WithObjectCode(model.EventExerciseObjectCode).
						WithMessage("Event exercise variant mode is invalid").WithDetailCode(2)
	ErrEventExerciseExists = err.ErrObjectExists.WithObjectCode(model.EventExerciseObjectCode).
				WithMessage("Exercise is already attached to this event").WithDetailCode(3)
	ErrEventExerciseNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("Event exercise not found").WithDetailCode(4)
	ErrEventExerciseVersionNotPublished = err.ErrConflict.WithObjectCode(model.EventExerciseObjectCode).
						WithMessage("Only a published exercise version can be attached").WithDetailCode(5)
	ErrEventExerciseNotActive = err.ErrConflict.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("Event exercise revision is no longer active").WithDetailCode(6)
	ErrEventExerciseVersionMismatch = err.ErrInvalidData.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("Replacement version belongs to a different exercise").WithDetailCode(7)
	ErrEventExerciseInfrastructureNotAllowed = err.ErrConflict.WithObjectCode(model.EventExerciseObjectCode).
							WithMessage("Exercises with infrastructure are not allowed for this event").WithDetailCode(8)
	ErrEventExerciseTaskHasAttempts = err.ErrConflict.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("The new version removes a task that teams already attempted").WithDetailCode(9)
	ErrEventExerciseDetachNeedsConfirm = err.ErrConflict.WithObjectCode(model.EventExerciseObjectCode).
						WithMessage("Teams already attempted this exercise: confirm to detach it").WithDetailCode(10)
	ErrEventExerciseNotAvailable = err.ErrForbidden.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("This exercise is not available to the event").WithDetailCode(11)
	ErrEventExerciseNoForkSource = err.ErrConflict.WithObjectCode(model.EventExerciseObjectCode).
					WithMessage("Only a catalog exercise can be forked, and only a fork can be reverted").WithDetailCode(12)
)
