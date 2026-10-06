package vpnModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// VPNConfigObjectCode — next free detail code: 3
var (
	ErrVPNScopeInvalid = err.ErrInvalidData.WithObjectCode(model.VPNConfigObjectCode).
				WithMessage("VPN config scope is invalid").WithDetailCode(1)
	ErrVPNSecretsNotConfigured = err.ErrConflict.WithObjectCode(model.VPNConfigObjectCode).
					WithMessage("Secret storage is not configured").WithDetailCode(2)
)
