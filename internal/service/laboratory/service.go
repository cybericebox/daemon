package laboratoryService

import (
	"github.com/cybericebox/daemon/internal/service/laboratory/lab"
	"github.com/cybericebox/daemon/internal/service/laboratory/vpn"
)

type (
	LaboratoryService struct {
		*vpnService.VPNService
		*labService.LabService
	}
	IRepository interface {
		vpnService.IRepository
		labService.IRepository
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *LaboratoryService {
	return &LaboratoryService{
		VPNService: vpnService.NewService(vpnService.Dependencies{Repository: deps.Repository}),
		LabService: labService.NewService(labService.Dependencies{Repository: deps.Repository}),
	}
}
