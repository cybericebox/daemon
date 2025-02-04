package vpn

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	laboratoryModel "github.com/cybericebox/daemon/internal/model/laboratory"
	"github.com/cybericebox/wireguard/pkg/controller/grpc/client"
	"github.com/cybericebox/wireguard/pkg/controller/grpc/protobuf"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"time"
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
		return nil, model.ErrVPNServer.WithError(err).WithMessage("Failed to create VPN client").Err()
	}

	//if _, err = c.Ping(context.Background(), &protobuf.EmptyRequest{}); err != nil {
	//	return nil, model.ErrVPN.WithError(err).WithMessage("Failed to ping VPN").Err()
	//}

	return c, nil
}

func (r *VPNRepository) GetVPNClients(ctx context.Context, userID, groupID uuid.UUID) ([]*laboratoryModel.VPNClient, error) {
	resp, err := r.client.GetClients(ctx, &protobuf.ClientsRequest{
		UserID:  userID.String(),
		GroupID: groupID.String(),
	})
	if err != nil {
		return nil, model.ErrVPNServer.WithError(err).WithMessage("Failed to get clients").WithContext("userID", userID).WithContext("groupID", groupID).Err()
	}

	clients := make([]*laboratoryModel.VPNClient, 0, len(resp.GetClients()))
	for _, c := range resp.GetClients() {
		clients = append(clients, &laboratoryModel.VPNClient{
			UserID:   uuid.FromStringOrNil(c.GetUserID()),
			GroupID:  uuid.FromStringOrNil(c.GetGroupID()),
			Banned:   c.GetBanned(),
			LastSeen: time.Unix(c.GetLastSeen(), 0),
		})
	}

	return clients, nil
}

func (r *VPNRepository) GetVPNClientConfig(ctx context.Context, userID, groupID uuid.UUID, destCIDR string) (string, error) {
	resp, err := r.client.GetClientConfig(ctx, &protobuf.ClientConfigRequest{
		UserID:   userID.String(),
		GroupID:  groupID.String(),
		DestCIDR: destCIDR,
	})
	if err != nil {
		return "", model.ErrVPNServer.WithError(err).WithMessage("Failed to get client config").WithContext("userID", userID).WithContext("groupID", groupID).WithContext("destCIDR", destCIDR).Err()
	}

	return resp.GetConfig(), nil
}

func (r *VPNRepository) DeleteVPNClients(ctx context.Context, userID, groupID uuid.UUID) error {
	if _, err := r.client.DeleteClients(ctx, &protobuf.ClientsRequest{
		UserID:  userID.String(),
		GroupID: groupID.String(),
	}); err != nil {
		return model.ErrVPNServer.WithError(err).WithMessage("Failed to delete client").WithContext("userID", userID).WithContext("groupID", groupID).Err()
	}

	return nil
}

func (r *VPNRepository) BanVPNClients(ctx context.Context, userID, groupID uuid.UUID) error {
	if _, err := r.client.BanClients(ctx, &protobuf.ClientsRequest{
		UserID:  userID.String(),
		GroupID: groupID.String(),
	}); err != nil {
		return model.ErrVPNServer.WithError(err).WithMessage("Failed to ban client").WithContext("userID", userID).WithContext("groupID", groupID).Err()
	}

	return nil
}

func (r *VPNRepository) UnBanVPNClients(ctx context.Context, userID, groupID uuid.UUID) error {
	if _, err := r.client.UnBanClients(ctx, &protobuf.ClientsRequest{
		UserID:  userID.String(),
		GroupID: groupID.String(),
	}); err != nil {
		return model.ErrVPNServer.WithError(err).WithMessage("Failed to unban client").WithContext("userID", userID).WithContext("groupID", groupID).Err()
	}

	return nil
}

func (r *VPNRepository) Close() {
	if err := r.client.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close VPN repository")
	}
}
