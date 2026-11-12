package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// Node profile errors. Those about one device carry the public context variant (1-based position in the version)
// and device (name); every one carries profile.
var (
	// ErrNodeProfileInvalid: the profile is not container, microvm or vm, or a device that is not a node has one.
	ErrNodeProfileInvalid = err.ErrInvalidData.WithObjectCode(model.NodeProfileObjectCode).
				WithMessage("Unknown node profile, or a profile on a device that is not a node").
				WithDetailCode(1)
	// ErrNodeProfileNotSupported: the platform does not support the profile of a node (the setting «Профілі вузлів»).
	ErrNodeProfileNotSupported = err.ErrInvalidData.WithObjectCode(model.NodeProfileObjectCode).
					WithMessage("The platform does not support this node profile").WithDetailCode(2)
	// ErrNodeProfileUnavailable: no connected laboratory agent can run the profile now.
	ErrNodeProfileUnavailable = err.ErrConflict.WithObjectCode(model.NodeProfileObjectCode).
					WithMessage("No connected laboratory agent can run this node profile now").
					WithDetailCode(3)
	// ErrNodeProfileVMUnsupported: the VM profile is in the model only and cannot be run yet.
	ErrNodeProfileVMUnsupported = err.ErrConflict.WithObjectCode(model.NodeProfileObjectCode).
					WithMessage("The VM profile cannot be run yet").WithDetailCode(4)
	// ErrNodeProfilePersistence: state persistence is not offered for the microvm and vm profiles. The context field is
	// "persistence" (the device's persistence block) next to device and profile.
	ErrNodeProfilePersistence = err.ErrInvalidData.WithObjectCode(model.NodeProfileObjectCode).
					WithMessage("State persistence is not available for devices with the microvm or vm profile").
					WithDetailCode(5)
)
