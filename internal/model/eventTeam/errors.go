package eventTeamModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// EventTeamObjectCode — next free detail code: 25
var (
	ErrEventTeamIdentityInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Event team event and captain identifiers are required").WithDetailCode(1)
	ErrEventTeamNameInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("Event team name is invalid").WithDetailCode(2)
	ErrEventTeamJoinCodeInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Event team join code is invalid").WithDetailCode(3)
	ErrEventTeamCaptainRequired = err.ErrForbidden.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Only the team captain may perform this action").WithDetailCode(4)
	ErrEventTeamCaptainInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("New team captain is invalid").WithDetailCode(5)
	ErrEventTeamFull = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("Event team is full").WithDetailCode(6)
	ErrEventTeamNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("Event team not found").WithDetailCode(7)
	ErrEventTeamParticipationInvalid = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
						WithMessage("Event is not configured for team participation").WithDetailCode(8)
	ErrEventTeamExists = err.ErrObjectExists.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("Event team already exists").WithDetailCode(9)
	ErrEventTeamRosterLocked = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Team roster is frozen after the event start; only a moderator can change it").WithDetailCode(10)
	ErrEventTeamCaptainMustTransfer = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Team captain must transfer captaincy before leaving").WithDetailCode(11)
	ErrEventTeamFieldsInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Team fields are invalid").WithDetailCode(12)
	ErrEventTeamNotAdmitted = err.ErrForbidden.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("The team is not admitted: it has fewer members than the event minimum").WithDetailCode(13)
	ErrEventTeamFieldNotEditable = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("This team field cannot be changed after the team is created").WithDetailCode(14)
	ErrEventTeamFieldsLocked = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Team fields cannot be changed after the event finishes").WithDetailCode(15)
	ErrEventTeamJoinCodeExpired = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("The team join link has expired").WithDetailCode(16)
	ErrEventTeamJoinCodeExpiryInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
						WithMessage("The join link expiry is invalid").WithDetailCode(17)
	ErrEventTeamStaffFieldsInvalid = err.ErrInvalidData.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("Staff-only fields are invalid").WithDetailCode(18)
	ErrEventTeamModeratorsLocked = err.ErrForbidden.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("The moderators team is always hidden and cannot be changed").WithDetailCode(19)
	ErrEventTeamSwitchLocked = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("The team is formed: its members cannot move to another team").WithDetailCode(20)
	ErrEventTeamFormed = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("The team is formed: its roster is closed").WithDetailCode(21)
	ErrEventTeamBelowMinimum = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
					WithMessage("The team has fewer members than the event minimum and cannot be formed").WithDetailCode(22)
	ErrEventTeamNotFormed = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("The team is not formed yet: tasks and laboratories open after the captain confirms the roster").WithDetailCode(23)
	ErrEventTeamLeaveLocked = err.ErrConflict.WithObjectCode(model.EventTeamObjectCode).
				WithMessage("The team is formed: a member cannot leave it on their own, ask the captain or the organizers").WithDetailCode(24)
)
