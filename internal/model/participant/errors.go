package participantModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// ParticipantObjectCode — next free detail code: 28 (27 retired: invitations are not rate limited)
var (
	ErrRegistrationClosed = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Registration is closed for this event").WithDetailCode(1)
	ErrRegistrationAfterStartForbidden = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
						WithMessage("Registration after the event has started is not allowed").WithDetailCode(2)
	ErrAlreadyParticipant = err.ErrObjectExists.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Already participating in this event").WithDetailCode(3)
	ErrParticipantNotFound = err.ErrObjectNotFound.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Participant not found").WithDetailCode(4)
	ErrParticipantNotPending = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Participant is not pending").WithDetailCode(5)
	ErrParticipantNotApproved = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Only an approved participant may join a team").WithDetailCode(6)
	ErrParticipantTeamInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Participant team assignment is invalid").WithDetailCode(7)
	ErrParticipantAccessForbidden = err.ErrForbidden.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Approved event participation is required").WithDetailCode(8)
	ErrParticipantFormRequired = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Complete the required participant form before joining").WithDetailCode(9)
	ErrInvitationRequired = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("A pending invitation is required").WithDetailCode(10)
	ErrInvitationEmailInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Invitation email is invalid").WithDetailCode(11)
	ErrTeamInvitationUnavailable = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("The invited team is no longer available").WithDetailCode(12)
	ErrInvitationExpired = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("The invitation has expired: the registration window is closed").WithDetailCode(13)
	ErrPseudonymsDisabled = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Pseudonyms are disabled for this event").WithDetailCode(14)
	ErrPseudonymInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Pseudonym must be 2 to 32 characters without control characters").WithDetailCode(15)
	ErrPseudonymTaken = err.ErrObjectExists.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("This pseudonym is already taken in this event").WithDetailCode(16)
	ErrPseudonymLocked = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Pseudonym can be changed only before the event starts").WithDetailCode(17)
	ErrEventFormRequired = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("A required event form must be completed before continuing").WithDetailCode(18)
	ErrParticipantKindInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Participant list kind must be participants, applications or invitations").WithDetailCode(19)
	ErrParticipantFieldNotEditable = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("This answer cannot be changed").WithDetailCode(20)
	ErrParticipantFieldsLocked = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Answers cannot be changed after the event finish").WithDetailCode(21)
	ErrParticipantAnswersInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Participant answers are invalid").WithDetailCode(22)
	ErrParticipantFieldPrefilled = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("This answer was filled in by the organizers and cannot be changed").WithDetailCode(23)
	ErrParticipantStaffFieldsInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
						WithMessage("Staff-only fields are invalid").WithDetailCode(24)
	ErrStaffCannotParticipate = err.ErrForbidden.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("Event owners and moderators cannot register as participants").WithDetailCode(25)
	ErrRegistrationNotOpen = err.ErrConflict.WithObjectCode(model.ParticipantObjectCode).
				WithMessage("Registration is not open: the event is not published yet, or it has finished or been withdrawn").WithDetailCode(26)
	ErrParticipantFieldRequired = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("A required form field is not filled in").WithDetailCode(27)
	ErrParticipantFormInvalid = err.ErrInvalidData.WithObjectCode(model.ParticipantObjectCode).
					WithMessage("The form is invalid").WithDetailCode(28)
)

// DetailField names the form field key an answer error is about.
const DetailField = "field"
