package ipam

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`.
//
// IPAMObjectCode — next free detail code: 2
var (
	// multi-site: the generic infrastructure failure for every IPAM operation
	// (always 500); per-site reason via WithMessage/WithError.
	ErrIPAM = err.ErrInternal.WithObjectCode(model.IPAMObjectCode)

	ErrIPAMCIDRRequired = err.ErrInvalidData.WithObjectCode(model.IPAMObjectCode).
				WithMessage("CIDR is required").WithDetailCode(1)
)
