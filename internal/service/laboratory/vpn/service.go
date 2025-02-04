package vpnService

import (
	"context"
	"github.com/cybericebox/daemon/internal/model/laboratory"
	"github.com/gofrs/uuid"
)

type (
	VPNService struct {
		repository IRepository
	}

	IRepository interface {
		GetVPNClientConfig(ctx context.Context, userID, groupID uuid.UUID, destCIDR string) (string, error)
		DeleteVPNClients(ctx context.Context, userID, groupID uuid.UUID) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *VPNService {
	return &VPNService{
		repository: deps.Repository,
	}
}

func (s *VPNService) GetVPNClientConfig(ctx context.Context, userID, groupID uuid.UUID, labCIDR string) (string, error) {
	config, err := s.repository.GetVPNClientConfig(ctx, userID, groupID, labCIDR)
	if err != nil {
		return "", laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to get VPN client config").Err()
	}

	return config, nil
}

func (s *VPNService) DeleteVPNClients(ctx context.Context, userID, groupID uuid.UUID) error {
	if err := s.repository.DeleteVPNClients(ctx, userID, groupID); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to delete VPN client").Err()
	}

	return nil
}
