package participantPreviewModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// next free detail code: 6
var (
	ErrGroupNameInvalid      = err.ErrInvalidData.WithObjectCode(model.ParticipantPreviewObjectCode).WithMessage("Preview group name must contain 1 to 100 characters without control characters").WithDetailCode(5)
	ErrTypedSettingsRequired = err.ErrInvalidData.WithObjectCode(model.ParticipantPreviewObjectCode).WithMessage("Use participant preview settings to select published exercises").WithDetailCode(4)
	ErrUnavailable           = err.ErrObjectNotFound.WithObjectCode(model.ParticipantPreviewObjectCode).WithMessage("Preview selection or active run not found").WithDetailCode(1)
	ErrInvalid               = err.ErrInvalidData.WithObjectCode(model.ParticipantPreviewObjectCode).WithMessage("Preview requires unique published exercises and fixed variants").WithDetailCode(2)
	ErrStartRequired         = err.ErrConflict.WithObjectCode(model.ParticipantPreviewObjectCode).WithMessage("Start the preview laboratory first").WithDetailCode(3)
)
