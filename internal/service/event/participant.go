package event

import (
	"context"
)

type (
	IParticipantRepository interface {
		GetVPNClientConfig(ctx context.Context, clientID, destCIDR string) (string, error)
	}
)

func (s *EventService) GetParticipantVPNConfig(ctx context.Context, participantID, labCIDR string) (string, error) {
	return s.repository.GetVPNClientConfig(ctx, participantID, labCIDR)
}
