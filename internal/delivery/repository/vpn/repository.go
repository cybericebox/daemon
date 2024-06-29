package vpn

import (
	"context"
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
	return client.NewWireguardConnection(client.Config{
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
}

func (r *VPNRepository) GetVPNClientConfig(ctx context.Context, clientID, destCIDR string) (string, error) {
	resp, err := r.WireguardClient.GetClientConfig(ctx, &protobuf.ClientConfigRequest{
		Id:       clientID,
		DestCIDR: destCIDR,
	})
	if err != nil {
		return "", err
	}

	return resp.GetConfig(), nil
}

func (r *VPNRepository) DeleteClient(ctx context.Context, clientID string) error {
	_, err := r.WireguardClient.DeleteClient(ctx, &protobuf.ClientRequest{Id: clientID})
	return err
}
