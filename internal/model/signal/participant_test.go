package signalModel

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestParticipantPayloadRegistryRoundTripsRoutingAndSnapshot(t *testing.T) {
	t.Parallel()

	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	payload := ParticipantPayload{
		ScopeEventID: eventID, SubjectUserID: userID,
		EventName: "CyberICEBox 2026", EventTag: "olympiad-2026",
		Registration: RegistrationOpen,
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	decoded, err := DefaultRegistry.Decode(TypeParticipantOpenRegistrationCompleted, raw)
	require.NoError(t, err)
	require.Equal(t, Routing{ScopeEventID: eventID, SubjectUserID: &userID}, decoded.Routing())
	got, ok := decoded.(*ParticipantPayload)
	require.True(t, ok)
	require.Equal(t, payload.EventName, got.EventName)
	require.Equal(t, payload.Registration, got.Registration)
}
