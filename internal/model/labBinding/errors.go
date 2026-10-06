package labBindingModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

var ErrLabBindingInvalid = err.ErrInvalidData.WithObjectCode(model.InfrastructureObjectCode).WithMessage("Lab binding data is invalid").WithDetailCode(2)
