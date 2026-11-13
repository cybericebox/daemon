package keyrotation_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/secretctx"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
)

// The re-encryption opens every value with the context the code sealed it with: the contexts it uses are the builders of
// internal/secretctx, not a second copy that could drift.
func TestContextsAreTheBuildersOfSecretctx(t *testing.T) {
	c := keyrotation.Contexts()
	id := uuid.FromStringOrNil("00000000-0000-4000-8000-000000000001")
	user, ref := uuid.FromStringOrNil("00000000-0000-4000-8000-000000000004"), uuid.FromStringOrNil("00000000-0000-4000-8000-000000000005")
	require.Equal(t, secretctx.SMTPPassword(id), c.SMTP(id))
	require.Equal(t, secretctx.AgentSecret(id, "key"), c.AgentKey(id))
	require.Equal(t, secretctx.AgentSecret(id, "access"), c.AgentAccess(id))
	require.Equal(t, []byte(secretctx.RegistryPush), c.RegistryPush)
	require.Equal(t, []byte(secretctx.RegistryPull), c.RegistryPull)
	require.Equal(t, secretctx.EventRegistry(id), c.EventRegistry(id))
	require.Equal(t, secretctx.VPNConfig(user, "event", uuid.NullUUID{UUID: ref, Valid: true}), c.VPN(user, "event", uuid.NullUUID{UUID: ref, Valid: true}))
	require.Equal(t, secretctx.ExerciseEnv(id, "web", "A"), c.ExerciseEnv(id, "web", "A"))
}
