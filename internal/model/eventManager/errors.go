package eventManagerModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// EventManagerObjectCode — next free detail code: 7
var (
	ErrEventManagerIdentityInvalid = err.ErrInvalidData.WithObjectCode(model.EventManagerObjectCode).
					WithMessage("Event manager event and user identifiers are required").WithDetailCode(1)
	ErrEventManagerRoleInvalid = err.ErrInvalidData.WithObjectCode(model.EventManagerObjectCode).
					WithMessage("Event manager role is invalid").WithDetailCode(2)
	ErrEventManagerNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventManagerObjectCode).
				WithMessage("Event manager membership not found").WithDetailCode(3)
	ErrEventManagementForbidden = err.ErrForbidden.WithObjectCode(model.EventManagerObjectCode).
					WithMessage("User cannot manage this event").WithDetailCode(4)
	ErrEventManagerOwnerProtected = err.ErrForbidden.WithObjectCode(model.EventManagerObjectCode).
					WithMessage("Event owner role cannot be changed or removed").WithDetailCode(5)
	ErrEventManagerViewerRedundant = err.ErrInvalidData.WithObjectCode(model.EventManagerObjectCode).
					WithMessage("Platform administrators already have read access to all events").WithDetailCode(6)
)
