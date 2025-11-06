package model

import (
	"github.com/cybericebox/lib/pkg/err"
)

var (
	ErrPlatform = err.ErrInternal.WithObjectCode(PlatformObjectCode)

	ErrUserNotFoundInContext      = ErrPlatform.WithMessage("User not found in context").WithDetailCode(1)
	ErrUserRoleNotFoundInContext  = ErrPlatform.WithMessage("User role not found in context").WithDetailCode(2)
	ErrSubdomainNotFoundInContext = ErrPlatform.WithMessage("Subdomain not found in context").WithDetailCode(3)
	ErrErrorNotFoundInContext     = ErrPlatform.WithMessage("Error not found in context").WithDetailCode(4)
)
