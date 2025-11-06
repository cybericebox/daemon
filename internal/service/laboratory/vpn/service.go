package vpnService

import (
	"context"

	"github.com/cybericebox/daemon/internal/model"
	laboratoryModel "github.com/cybericebox/daemon/internal/model/laboratory"
)

type (
	VPNService struct {
		repository IRepository
	}

	IRepository interface {
		GetVPNClients(ctx context.Context, ids []string) (
			[]*laboratoryModel.VPNClient,
			error,
		)

		GetVPNClientConfig(ctx context.Context, ids []string) (string, error)
		RemoveVPNClients(ctx context.Context, ids []string) error

		BanVPNClients(ctx context.Context, ids []string) error
		UnBanVPNClients(ctx context.Context, ids []string) error

		AddVPNClientsDestCIDRs(ctx context.Context, ids []string, destCIDRs []string) error
		RemoveVPNClientsDestCIDRs(ctx context.Context, ids []string, destCIDRs []string) error
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

func (s *VPNService) GetVPNClientConfig(ctx context.Context, ids laboratoryModel.VPNClientIDs) (
	string,
	error,
) {
	config, err := s.repository.GetVPNClientConfig(ctx, ids.StringIDS())
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get VPN client config").Err()
	}

	return config, nil
}

func (s *VPNService) RemoveVPNClients(ctx context.Context, ids laboratoryModel.VPNClientIDs) error {
	if err := s.repository.RemoveVPNClients(ctx, ids.StringIDS()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete VPN client").Err()
	}

	return nil
}

func (s *VPNService) BanVPNClients(ctx context.Context, ids laboratoryModel.VPNClientIDs) error {
	if err := s.repository.BanVPNClients(ctx, ids.StringIDS()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to ban VPN client").Err()
	}

	return nil
}

func (s *VPNService) UnBanVPNClients(ctx context.Context, ids laboratoryModel.VPNClientIDs) error {
	if err := s.repository.UnBanVPNClients(ctx, ids.StringIDS()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unban VPN client").Err()
	}

	return nil
}

func (s *VPNService) AddVPNClientsDestCIDRs(
	ctx context.Context,
	ids laboratoryModel.VPNClientIDs,
	destCIDRs []string,
) error {
	if err := s.repository.AddVPNClientsDestCIDRs(ctx, ids.StringIDS(), destCIDRs); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to add VPN client dest CIDRs").Err()
	}

	return nil
}

func (s *VPNService) RemoveVPNClientsDestCIDRs(
	ctx context.Context,
	ids laboratoryModel.VPNClientIDs,
	destCIDRs []string,
) error {
	if err := s.repository.RemoveVPNClientsDestCIDRs(ctx, ids.StringIDS(), destCIDRs); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove VPN client dest CIDRs").Err()
	}

	return nil
}

func (s *VPNService) GetVPNClients(ctx context.Context, ids laboratoryModel.VPNClientIDs) (
	[]*laboratoryModel.VPNClient,
	error,
) {
	clients, err := s.repository.GetVPNClients(ctx, ids.StringIDS())
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get VPN clients").Err()
	}

	return clients, nil
}
