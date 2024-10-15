package vpn

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
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
		return nil, model.ErrVPN.WithError(err).WithMessage("Failed to create VPN client").Cause()
	}

	if _, err = c.Ping(context.Background(), &protobuf.EmptyRequest{}); err != nil {
		return nil, model.ErrVPN.WithError(err).WithMessage("Failed to ping VPN").Cause()
	}

	return c, nil
}

func (r *VPNRepository) GetVPNClientConfig(ctx context.Context, clientID, destCIDR string) (string, error) {
	resp, err := r.WireguardClient.GetClientConfig(ctx, &protobuf.ClientConfigRequest{
		Id:       clientID,
		DestCIDR: destCIDR,
	})
	if err != nil {
		return "", model.ErrVPN.WithError(err).WithMessage("Failed to get client config").WithContext("clientID", clientID).WithContext("destCIDR", destCIDR).Cause()
	}

	return resp.GetConfig(), nil
}

func (r *VPNRepository) DeleteVPNClient(ctx context.Context, clientID string) error {
	if _, err := r.WireguardClient.DeleteClient(ctx, &protobuf.ClientRequest{Id: clientID}); err != nil {
		return model.ErrVPN.WithError(err).WithMessage("Failed to delete client").WithContext("clientID", clientID).Cause()
	}

	return nil
}
