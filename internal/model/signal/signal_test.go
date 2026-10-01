package signalModel_test

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

const testSignalType signalModel.Type = "participant.open_registration.completed"

type registrationPayload struct {
	ScopeEventID  uuid.UUID `json:"scope_event_id"`
	SubjectUserID uuid.UUID `json:"subject_user_id"`
}

func (p registrationPayload) Routing() signalModel.Routing {
	return signalModel.Routing{ScopeEventID: p.ScopeEventID, SubjectUserID: &p.SubjectUserID}
}

func TestRegistryDecodesRegisteredPayload(t *testing.T) {
	registry := signalModel.NewRegistry()
	registry.Register(testSignalType, func() signalModel.Payload { return &registrationPayload{} })

	eventID := uuid.Must(uuid.NewV4())
	userID := uuid.Must(uuid.NewV4())
	raw, err := json.Marshal(registrationPayload{ScopeEventID: eventID, SubjectUserID: userID})
	require.NoError(t, err)

	payload, err := registry.Decode(testSignalType, raw)
	require.NoError(t, err)
	require.Equal(t, eventID, payload.Routing().ScopeEventID)
	require.Equal(t, userID, *payload.Routing().SubjectUserID)
}

func TestRegistryRejectsUnknownType(t *testing.T) {
	_, err := signalModel.NewRegistry().Decode("unknown", json.RawMessage(`{}`))
	require.Error(t, err)
}
