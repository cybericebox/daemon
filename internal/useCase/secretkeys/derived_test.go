package secretkeys

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	"github.com/cybericebox/daemon/pkg/secret"
)

// After the move to a master key the platform data is partly still sealed with the old explicit key: the report tells the two
// kinds of key apart, in the key list and in the counts, and counts the old ones as what re-encryption has to move.
func TestStatusTellsDerivedKeysFromExplicitOnes(t *testing.T) {
	oldRing := ring(t, hexOld) // the single key of the older configuration: id "default"
	spec, err := secret.Resolve("m1:"+hexNew, hexOld, secret.PurposePlatform, false)
	require.NoError(t, err)
	platform := ring(t, spec)

	store := sealedBy(t, platform, platform, platform)
	store.smtp = seal(t, oldRing, "smtp", mailUseCase.PasswordContext(idSMTP)) // sealed before the master key
	rep, err := New(store, Ciphers{Platform: platform, VPN: platform, Exercise: platform}).Check(context.Background(), true)
	require.NoError(t, err)
	require.Zero(t, rep.Problems(), "the old key still opens its values")
	require.Equal(t, map[string]int{"default": 1}, column(t, rep, "mail_smtp_configs.password_ciphertext").ByKey)
	require.Equal(t, map[string]int{"d-m1": 1}, column(t, rep, "infrastructure_agents.client_key_ciphertext").ByKey)

	var out bytes.Buffer
	rep.Write(&out)
	text := out.String()
	require.Contains(t, text, `d-m1 (derived from master m1), default (explicit)`)
	require.Contains(t, text, `key default (explicit): 1`)
	require.Contains(t, text, `key d-m1 (derived from master m1): 1`)
}

// Dropping the master version that sealed values is the lost key the check exists to find.
func TestDroppingAMasterVersionIsFound(t *testing.T) {
	both, err := secret.Resolve("m2:"+hexNew+",m1:"+hexOld, "", secret.PurposeVPN, false)
	require.NoError(t, err)
	onlyNew, err := secret.Resolve("m2:"+hexNew, "", secret.PurposeVPN, false)
	require.NoError(t, err)
	sealing, loaded := ring(t, both), ring(t, onlyNew)

	// the values were sealed with m1 while it was current
	oldMaster, err := secret.Resolve("m1:"+hexOld, "", secret.PurposeVPN, false)
	require.NoError(t, err)
	store := sealedBy(t, sealing, ring(t, oldMaster), sealing)
	rep, err := New(store, Ciphers{Platform: sealing, VPN: loaded, Exercise: sealing}).Check(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Problems())
	require.Equal(t, KeyNotLoaded, column(t, rep, "user_vpn_configs.config").ProblemRefs[0].Kind)
	require.Equal(t, "d-m1", column(t, rep, "user_vpn_configs.config").ProblemRefs[0].KeyID)
}
