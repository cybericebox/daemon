package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
)

type (
	IParticipantRepository interface {
		GetVPNClientConfig(ctx context.Context, clientID, destCIDR string) (string, error)
	}
)

func (s *EventService) GetParticipantVPNConfig(ctx context.Context, participantID, labCIDR string) (string, error) {
	config, err := s.repository.GetVPNClientConfig(ctx, participantID, labCIDR)
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to get VPN client config")
	}

	return config, nil
}
