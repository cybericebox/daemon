// internal/model/eventConfig/errors.go
package eventConfigModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/exercise/errors.go. Enforced by
// `make lint-errors`. Each var has exactly one non-test call site.
//
// EventConfigObjectCode — next free detail code: 23
var (
	ErrParticipationLocked = err.ErrConflict.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Participation type cannot be changed after publication").WithDetailCode(1)
	ErrParticipationInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Participation type is invalid").WithDetailCode(2)
	ErrRegistrationInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Registration type is invalid").WithDetailCode(3)
	ErrVisibilityInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Visibility value is invalid").WithDetailCode(4)
	ErrPreviewDescriptionTooLong = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Preview description is too long").WithDetailCode(5)
	// note: preview picture reuses no new base; picks the next detail code.
	ErrPreviewPictureInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Preview picture must be empty or a valid https URL").WithDetailCode(6)
	// ErrEventConfigNotFound is used only by mutateEventConfig's zero-rows
	// re-read (internal/useCase/event): normal reads self-heal a missing
	// config via lazy creation (GetEventConfig), so this only fires if the
	// config row disappears between the mutate's initial fetch and its write
	// (e.g. the parent event was concurrently deleted, cascading the row).
	ErrEventConfigNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Event config not found").WithDetailCode(7)
	ErrTeamLimitsInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Event team limits are invalid").WithDetailCode(8)
	ErrThemeInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
			WithMessage("Event theme needs a #RRGGBB brand with 4.5:1 white contrast and an optional #RRGGBB accent").WithDetailCode(9)
	ErrParticipationRequired = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Select the participation type before scheduling publication").WithDetailCode(10)
	ErrTeamSizeLocked = err.ErrConflict.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Team size cannot be changed after publication").WithDetailCode(11)
	// Detail code 12 (dynamic laboratory mode locked) is retired: the
	// infrastructure flag is admin-owned and immutable after creation (W5).
	// Results read denials: distinct codes let clients show why results are
	// unavailable (see ResultsAvailability in useCase/event).
	ErrResultsHidden = err.ErrForbidden.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Results are hidden by the organizer").WithDetailCode(13)
	ErrResultsParticipantsOnly = err.ErrForbidden.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Results are visible to approved participants only").WithDetailCode(14)
	ErrResultsNotStarted = err.ErrForbidden.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("Results are available after the event starts").WithDetailCode(15)
	ErrListColumnsInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("List column configuration is invalid").WithDetailCode(16)
	ErrResultsSettingsInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Results settings are invalid").WithDetailCode(17)
	// The live screen (view=live) is a moderator's projector view (W9, L1).
	ErrResultsLiveScreenManagersOnly = err.ErrForbidden.WithObjectCode(model.EventConfigObjectCode).
						WithMessage("The live screen is available to event managers only").WithDetailCode(18)
	ErrHintChargeModeInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Hint charge mode must be reward or balance").WithDetailCode(19)
	ErrCountdownSettingsInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Finish countdown minutes must be between 1 and 1440").WithDetailCode(20)

	// ErrTaskRevealModeInvalid: the task reveal mode is neither all_ready nor as_ready.
	ErrTaskRevealModeInvalid = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
					WithMessage("Task reveal mode must be all_ready or as_ready").WithDetailCode(21)
	// ErrTaskRevealModeLocked: the mode is chosen before the event starts; revealed tasks are never hidden.
	ErrTaskRevealModeLocked = err.ErrInvalidData.WithObjectCode(model.EventConfigObjectCode).
				WithMessage("The task reveal mode cannot be changed after the event starts").WithDetailCode(22)
)
