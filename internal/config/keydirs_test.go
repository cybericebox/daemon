package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	dirKeyA = "000102030405060708090a0b0c0d0e0f000102030405060708090a0b0c0d0e0f"
	dirKeyB = "101112131415161718191a1b1c1d1e1f101112131415161718191a1b1c1d1e1f"
)

func keyDir(t *testing.T, current string, keys map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for id, key := range keys {
		require.NoError(t, os.WriteFile(filepath.Join(dir, id+".key"), []byte(key), 0o400))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CURRENT"), []byte(current), 0o400))
	return dir
}

func TestResolveKeyDirsReplacesTheVariableOfEachFamily(t *testing.T) {
	var c Config
	c.Platform.SecretsKey = dirKeyA // a stale variable: the directory wins
	c.Platform.SecretsKeyDir = keyDir(t, "k2", map[string]string{"k1": dirKeyA, "k2": dirKeyB})
	c.VPN.SecretsKey = dirKeyB // no directory: the variable stays
	c.Auth.SessionEncryptionKeyDir = keyDir(t, "s1", map[string]string{"s1": dirKeyA})

	require.NoError(t, c.ResolveKeyDirs())
	require.Equal(t, "k2:"+dirKeyB+",k1:"+dirKeyA, c.Platform.SecretsKey)
	require.Equal(t, dirKeyB, c.VPN.SecretsKey)
	require.Equal(t, "s1:"+dirKeyA, c.Auth.SessionEncryptionKey)
	require.Empty(t, c.Exercise.SecretsKey)
}

func TestResolveKeyDirsNamesTheSettingOfABadDirectory(t *testing.T) {
	var c Config
	c.Exercise.SecretsKey = dirKeyA
	c.Exercise.SecretsKeyDir = filepath.Join(t.TempDir(), "missing")
	err := c.ResolveKeyDirs()
	require.ErrorContains(t, err, "EXERCISE_SECRETS_KEY_DIR")
	require.Equal(t, dirKeyA, c.Exercise.SecretsKey, "no silent change of the key on a failure")

	c.Exercise.SecretsKeyDir = keyDir(t, "nope", map[string]string{"k1": dirKeyA})
	require.ErrorContains(t, c.ResolveKeyDirs(), "EXERCISE_SECRETS_KEY_DIR")
}

func TestSecretsGuardModes(t *testing.T) {
	for _, mode := range []string{"warn", "refuse", "off"} {
		require.NoError(t, KeysConfig{SecretsGuard: mode}.Validate())
	}
	require.Error(t, KeysConfig{SecretsGuard: "maybe"}.Validate())
	require.Error(t, KeysConfig{}.Validate())
}
