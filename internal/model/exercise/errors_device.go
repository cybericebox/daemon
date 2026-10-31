package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// ErrDeviceNameReserved is raised when a device is named like an endpoint the platform provides in every lab. The public context
// names the variant (1-based position) and the device, so the editor can mark it.
var ErrDeviceNameReserved = err.ErrInvalidData.WithObjectCode(model.DeviceObjectCode).
	WithMessage("This device name is reserved by the platform").WithDetailCode(1)
