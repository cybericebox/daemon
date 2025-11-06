package authModel

import (
	"github.com/cybericebox/lib/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

var (
	ErrAuthInvalidRecaptchaToken  = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(1).WithMessage("Invalid recaptcha token")  // 20801
	ErrAuthNoRecaptchaToken       = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(2).WithMessage("No recaptcha token")       // 20802
	ErrAuthInvalidRecaptchaAction = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(3).WithMessage("Invalid recaptcha action") // 20803
	ErrAuthLowerScore             = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(4).WithMessage("Lower recaptcha score")    // 20804
)
