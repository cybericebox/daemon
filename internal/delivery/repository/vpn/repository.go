package vpn

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/wireguard/pkg/controller/grpc/client"
	"github.com/cybericebox/wireguard/pkg/controller/grpc/protobuf"
	"github.com/rs/zerolog/log"
)

type (
	VPNRepository struct {
		protobuf.WireguardClient
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

func newVPN(cfg *config.VPNGRPCConfig) (protobuf.WireguardClient, error) {
	c, err := client.NewWireguardConnection(client.Config{
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
	})
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to create VPN client")
	}

	return c, nil
}

func (r *VPNRepository) GetVPNClientConfig(ctx context.Context, clientID, destCIDR string) (string, error) {
	resp, err := r.WireguardClient.GetClientConfig(ctx, &protobuf.ClientConfigRequest{
		Id:       clientID,
		DestCIDR: destCIDR,
	})
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to get client config")
	}

	return resp.GetConfig(), nil
}

func (r *VPNRepository) DeleteClient(ctx context.Context, clientID string) error {
	if _, err := r.WireguardClient.DeleteClient(ctx, &protobuf.ClientRequest{Id: clientID}); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete client")
	}

	return nil
}
