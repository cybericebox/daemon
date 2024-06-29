package protection

import (
	"github.com/cybericebox/daemon/internal/config"
)

type (
	protection struct {
		config  *config.ProtectionConfig
		useCase IUseCase
	}
	IUseCase interface {
		IAuthProtectionUseCase
	}

	// Dependencies for the routes protection
	Dependencies struct {
		Config  *config.ProtectionConfig
		UseCase IUseCase
	}
)

var protector *protection

func InitProtection(deps *Dependencies) {
	protector = &protection{useCase: deps.UseCase, config: deps.Config}
}
