package authModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
)

var (
	ErrAuthRecaptcha                       = err.ErrInternal.WithObjectCode(model.AuthRecaptchaObjectCode)
	ErrAuthRecaptchaInvalidRecaptchaToken  = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(1).WithMessage("Invalid recaptcha token")  //20801
	ErrAuthRecaptchaNoRecaptchaToken       = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(2).WithMessage("No recaptcha token")       //20802
	ErrAuthRecaptchaInvalidRecaptchaAction = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(3).WithMessage("Invalid recaptcha action") //20803
	ErrAuthRecaptchaLowerScore             = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).WithDetailCode(4).WithMessage("Lower recaptcha score")    //20804
)
