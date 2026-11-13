package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// ErrDeviceNameReserved is raised when a device is named like an endpoint the platform provides in every lab. The public context
// names the variant (1-based position) and the device, so the editor can mark it.
var ErrDeviceNameReserved = err.ErrInvalidData.WithObjectCode(model.DeviceObjectCode).
	WithMessage("This device name is reserved by the platform").WithDetailCode(1)

// ErrNameHasSeparator is raised when the name of a device or of a variable of a device holds ':'. The two names are part of
// the context a secret variable is sealed with and are joined by ':', so a name with ':' could run into its neighbour
// (device "a:b", variable "c" and device "a", variable "b:c" would share a context). The public context names the variant
// (1-based), the device and the field ("device" or "env") so the editor can mark it.
var ErrNameHasSeparator = err.ErrInvalidData.WithObjectCode(model.DeviceObjectCode).
	WithMessage("A device or variable name must not contain ':'").WithDetailCode(2)
