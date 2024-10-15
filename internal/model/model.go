package model

var (
	ErrPlatformUserNotFoundInContext      = ErrPlatform.WithMessage("User not found in context").WithDetailCode(1)
	ErrPlatformUserRoleNotFoundInContext  = ErrPlatform.WithMessage("User role not found in context").WithDetailCode(2)
	ErrPlatformSubdomainNotFoundInContext = ErrPlatform.WithMessage("Subdomain not found in context").WithDetailCode(3)
	ErrPlatformErrorNotFoundInContext     = ErrPlatform.WithMessage("Error not found in context").WithDetailCode(4)
)
