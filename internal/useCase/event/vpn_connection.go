package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
)

// Read only this person's peer from the already collected group snapshot.
// No request to the agent and no other person's connection state leaves here.
func (u *EventUseCase) ownVPNConnection(ctx context.Context, eventID, userID uuid.UUID, group string) *bool {
	parsedEvent, teamID, ok := labBindingModel.ParseGroupName(group)
	if !ok || parsedEvent != eventID {
		return nil
	}
	row, err := u.repo.GetLabMonitoringCurrent(ctx, postgres.GetLabMonitoringCurrentParams{EventID: eventID, EventTeamID: teamID, LabGroupName: group})
	if err != nil {
		return nil
	}
	return vpnPeerConnected(row.Payload, userID, row.ObservedAt, time.Now())
}

func vpnPeerConnected(payload []byte, userID uuid.UUID, observedAt, now time.Time) *bool {
	// Unknown data must not turn a disconnected/unknown peer green.
	if observedAt.IsZero() || now.Sub(observedAt) > eventAnalyticsModel.VPNOnlineWindow || observedAt.After(now.Add(time.Minute)) {
		return nil
	}
	var snapshot struct {
		Clients []*struct {
			Name   string `json:"name"`
			Status struct {
				Statistics struct {
					LastHandshakeUnix json.RawMessage `json:"lastHandshakeUnix"`
				} `json:"statistics"`
			} `json:"status"`
		} `json:"clients"`
	}
	if json.Unmarshal(payload, &snapshot) != nil || snapshot.Clients == nil {
		return nil
	}
	connected, ownSeen := false, false
	for _, client := range snapshot.Clients {
		if client == nil || client.Name == "" {
			return nil
		}
		var handshake int64
		raw := client.Status.Statistics.LastHandshakeUnix
		if len(raw) != 0 {
			var number json.Number
			if string(raw) == "null" || json.Unmarshal(raw, &number) != nil || number == "" {
				return nil
			}
			var err error
			handshake, err = number.Int64()
			if err != nil || handshake < 0 || time.Unix(handshake, 0).After(now.Add(time.Minute)) {
				return nil
			}
		}
		if client.Name != labBindingModel.ParticipantClientName(userID) {
			continue
		}
		if ownSeen {
			return nil
		}
		ownSeen = true
		connected = handshake > 0 && eventAnalyticsModel.VPNOnline(time.Unix(handshake, 0), now)
	}
	return &connected
}
