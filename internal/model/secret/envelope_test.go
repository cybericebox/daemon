package secretModel

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeContextBindsItsImmutableMetadata(t *testing.T) {
	t.Parallel()

	envelope := Envelope{
		ID:           uuid.Must(uuid.NewV7()),
		Purpose:      PurposeNotification,
		KeyVersion:   1,
		SignalType:   "participant.invitation.sent",
		FieldPath:    "invitation.token",
		ScopeEventID: uuid.Must(uuid.NewV7()),
	}

	context, err := envelope.Context()
	require.NoError(t, err)
	require.NotEmpty(t, context)

	envelope.FieldPath = "invitation.other_token"
	otherContext, err := envelope.Context()
	require.NoError(t, err)
	require.NotEqual(t, context, otherContext)
}

func TestValueSerializesPlaintextThenOpaqueReference(t *testing.T) {
	t.Parallel()

	value := NewValue("invite-token")
	raw, err := value.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `"invite-token"`, string(raw))

	ref := Reference{ID: uuid.Must(uuid.NewV7()), Purpose: PurposeNotification, FieldPath: "invite.token"}
	value.SetReference(ref)
	raw, err = value.MarshalJSON()
	require.NoError(t, err)
	require.NotContains(t, string(raw), "invite-token")
	var restored Value
	require.NoError(t, restored.UnmarshalJSON(raw))
	require.Equal(t, ref, *restored.Reference())
}
