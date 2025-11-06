package vpn

import (
	"context"
	"time"

	"github.com/cybericebox/wireguard/pkg/controller/grpc/client"
	"github.com/cybericebox/wireguard/pkg/controller/grpc/protobuf"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	laboratoryModel "github.com/cybericebox/daemon/internal/model/laboratory"
)

type (
	VPNRepository struct {
		client client.WireguardClient
	}

	Dependencies struct {
		Config *config.VPNGRPCConfig
	}
)

func NewRepository(deps Dependencies) *VPNRepository {
	cl, err := newVPN(deps.Config)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create VPN client")
		return nil
	}

	return &VPNRepository{
		cl,
	}
}

func newVPN(cfg *config.VPNGRPCConfig) (client.WireguardClient, error) {
	c, err := client.NewWireguardConnection(
		client.Config{
			Endpoint: cfg.Endpoint,
			Auth: client.Auth{
				AuthKey: cfg.AuthKey,
				SignKey: cfg.SignKey,
			},
			TLS: client.TLS{
				Enabled:  cfg.TLS.Enabled,
				CertFile: cfg.TLS.CertFile,
				CertKey:  cfg.TLS.KeyFile,
			},
		},
	)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to create VPN client").Err()
	}

	if _, err = c.Ping(context.Background(), &protobuf.EmptyRequest{}); err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to ping VPN").Err()
	}

	return c, nil
}

func (r *VPNRepository) GetVPNClients(ctx context.Context, ids []string) (
	[]*laboratoryModel.VPNClient,
	error,
) {
	resp, err := r.client.GetClients(
		ctx, &protobuf.ClientsRequest{
			IDs: ids,
		},
	)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get VPN clients").WithContext(
			"IDS", ids,
		).Err()
	}

	clients := make([]*laboratoryModel.VPNClient, 0, len(resp.GetClients()))
	for _, c := range resp.GetClients() {
		clients = append(
			clients, &laboratoryModel.VPNClient{
				IDs:       laboratoryModel.ParseClientIDs(c.GetIDs()),
				IP:        c.GetAddress(),
				PublicKey: c.GetPublicKey(),
				DestCIDRs: c.GetDestCIDRs(),
				Banned:    c.GetBanned(),
				LastSeen:  time.Unix(c.GetLastSeen(), 0),
			},
		)
	}

	return clients, nil
}

func (r *VPNRepository) GetVPNClientConfig(ctx context.Context, ids []string) (
	string,
	error,
) {
	resp, err := r.client.GetClientConfig(
		ctx, &protobuf.ClientsRequest{
			IDs: ids,
		},
	)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get VPN client config").WithContext(
			"IDS", ids,
		).Err()
	}

	return resp.GetConfig(), nil
}

func (r *VPNRepository) RemoveVPNClients(ctx context.Context, ids []string) error {
	if _, err := r.client.RemoveClients(
		ctx, &protobuf.ClientsRequest{
			IDs: ids,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete clients").WithContext(
			"IDS", ids,
		).Err()
	}

	return nil
}

func (r *VPNRepository) AddVPNClientsDestCIDRs(ctx context.Context, ids []string, destCIDRs []string) error {
	if _, err := r.client.AddClientsDestCIDRs(
		ctx, &protobuf.ClientsDestCIDRsRequest{
			IDs:       ids,
			DestCIDRs: destCIDRs,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to add clients dest CIDRs").WithContext(
			"IDS", ids,
		).WithContext("DestCIDRs", destCIDRs).Err()
	}

	return nil
}

func (r *VPNRepository) RemoveVPNClientsDestCIDRs(ctx context.Context, ids []string, destCIDRs []string) error {
	if _, err := r.client.RemoveClientsDestCIDRs(
		ctx, &protobuf.ClientsDestCIDRsRequest{
			IDs:       ids,
			DestCIDRs: destCIDRs,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove clients dest CIDRs").WithContext(
			"IDS", ids,
		).WithContext("DestCIDRs", destCIDRs).Err()
	}

	return nil
}

func (r *VPNRepository) BanVPNClients(ctx context.Context, ids []string) error {
	if _, err := r.client.BanClients(
		ctx, &protobuf.ClientsRequest{
			IDs: ids,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to ban clients").WithContext(
			"IDS", ids,
		).Err()
	}

	return nil
}

func (r *VPNRepository) UnBanVPNClients(ctx context.Context, ids []string) error {
	if _, err := r.client.UnBanClients(
		ctx, &protobuf.ClientsRequest{
			IDs: ids,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unban clients").WithContext(
			"IDS", ids,
		).Err()
	}

	return nil
}

func (r *VPNRepository) Close() {
	if err := r.client.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close VPN repository")
	}
}
