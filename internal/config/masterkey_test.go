package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/pkg/secret"
)

func ids(t *testing.T, spec string) []string {
	t.Helper()
	c, err := secret.New(spec)
	require.NoError(t, err)
	return c.KeyIDs()
}

// A fresh install sets one key and gets a key for every purpose, each different.
func TestOneMasterKeyGivesEveryPurposeItsOwnKey(t *testing.T) {
	var c Config
	c.Keys.SecretsMasterKey = dirKeyA
	require.NoError(t, c.ResolveKeys())
	specs := map[string]string{
		"session": c.Auth.SessionEncryptionKey, "platform": c.Platform.SecretsKey,
		"vpn": c.VPN.SecretsKey, "exercise": c.Exercise.SecretsKey,
	}
	seen := map[string]string{}
	for name, spec := range specs {
		require.Equal(t, []string{"d-default"}, ids(t, spec), name)
		if other, dup := seen[spec]; dup {
			require.Failf(t, "two purposes got the same key", "%s and %s", name, other)
		}
		seen[spec] = name
	}
	// the same master gives the same keys again, as after a restart
	var again Config
	again.Keys.SecretsMasterKey = dirKeyA
	require.NoError(t, again.ResolveKeys())
	require.Equal(t, c.Platform.SecretsKey, again.Platform.SecretsKey)
}

func TestMasterKeyDirectoryWinsAndKeepsOldKeysAsOldEntries(t *testing.T) {
	var c Config
	c.Keys.SecretsMasterKey = dirKeyB // stale, the directory wins
	c.Keys.SecretsMasterKeyDir = keyDir(t, "m2", map[string]string{"m1": dirKeyA, "m2": dirKeyB})
	c.Platform.SecretsKey = dirKeyA // the key the database was sealed with before the master key existed
	c.VPN.SecretsKeyDir = keyDir(t, "v1", map[string]string{"v1": dirKeyB})

	require.NoError(t, c.ResolveKeys())
	require.Equal(t, []string{"d-m2", "d-m1", "default"}, ids(t, c.Platform.SecretsKey))
	require.Equal(t, []string{"d-m2", "d-m1", "v1"}, ids(t, c.VPN.SecretsKey))
	require.Equal(t, []string{"d-m2", "d-m1"}, ids(t, c.Exercise.SecretsKey))
}

func TestAnExplicitKeyCanKeepSealing(t *testing.T) {
	var c Config
	c.Keys.SecretsMasterKey = dirKeyA
	c.Exercise.SecretsKey = "own:" + dirKeyB
	c.Exercise.SecretsSeal = SealExplicit
	require.NoError(t, c.ResolveKeys())
	require.Equal(t, []string{"own", "d-default"}, ids(t, c.Exercise.SecretsKey))

	var none Config
	none.Keys.SecretsMasterKey = dirKeyA
	none.Platform.SecretsSeal = SealExplicit // nothing explicit to seal with
	require.ErrorContains(t, none.ResolveKeys(), "PLATFORM_SECRETS_KEY")
	var bad Config
	bad.Keys.SecretsMasterKey = dirKeyA
	bad.Platform.SecretsSeal = "sometimes"
	require.ErrorContains(t, bad.ResolveKeys(), "seal mode")
}

// Without a master key the older configuration works exactly as before.
func TestWithoutAMasterKeyEachPurposeKeepsItsOwnKey(t *testing.T) {
	var c Config
	c.Platform.SecretsKey = dirKeyA
	c.Auth.SessionEncryptionKeyDir = keyDir(t, "s1", map[string]string{"s1": dirKeyB})
	require.NoError(t, c.ResolveKeys())
	require.Equal(t, dirKeyA, c.Platform.SecretsKey)
	require.Equal(t, "s1:"+dirKeyB, c.Auth.SessionEncryptionKey)
	require.Empty(t, c.VPN.SecretsKey, "unset stays off")
}

func TestABadMasterChangesNothing(t *testing.T) {
	var c Config
	c.Keys.SecretsMasterKey = "abcd"
	c.Platform.SecretsKey = dirKeyA
	require.Error(t, c.ResolveKeys())
	require.Equal(t, dirKeyA, c.Platform.SecretsKey)
	require.Empty(t, c.VPN.SecretsKey)

	c.Keys.SecretsMasterKey = ""
	c.Keys.SecretsMasterKeyDir = keyDir(t, "nope", map[string]string{"m1": dirKeyA})
	require.ErrorContains(t, c.ResolveKeys(), "SECRETS_MASTER_KEY_DIR")
}
