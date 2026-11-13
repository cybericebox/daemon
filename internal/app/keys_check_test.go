package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/useCase/secretkeys"
)

var checkLifetimes = secretkeys.Lifetimes{SessionIdle: 12 * time.Hour, SessionAbsolute: 168 * time.Hour, EventRenderGrant: 5 * time.Minute}

func checkReport() secretkeys.Report {
	return secretkeys.Report{
		Opened:     true,
		SnapshotAt: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC),
		Keys: map[secretkeys.Family][]string{
			secretkeys.FamilyPlatform: {"k2", "k1"},
			secretkeys.FamilyVPN:      {"d-m2", "d-m1"},
			secretkeys.FamilyExercise: {"k2"},
		},
		Columns: []secretkeys.Column{{Family: secretkeys.FamilyPlatform, Name: "mail_smtp_configs.password_ciphertext", Total: 1, ByKey: map[string]int{"k2": 1}}},
	}
}

func locateNothing(secretkeys.Family, string) string { return "somewhere" }

// Exit codes: 0 retirable, 1 not yet, 2 cannot decide; the overview is informational.
func TestCheckCommand_ExitCodes(t *testing.T) {
	var out bytes.Buffer
	code, err := checkCommand(&out, checkReport(), "platform", "k1", locateNothing, checkLifetimes)
	require.NoError(t, err)
	require.Equal(t, CheckRetirable, code)
	require.Contains(t, out.String(), "RETIRABLE")

	rep := checkReport()
	rep.Columns[0].ByKey = map[string]int{"k1": 1}
	out.Reset()
	code, err = checkCommand(&out, rep, "platform", "k1", locateNothing, checkLifetimes)
	require.NoError(t, err)
	require.Equal(t, CheckNotYet, code)
	require.Contains(t, out.String(), "NOT YET")

	for _, c := range [][2]string{{"nope", "k1"}, {"platform", "k9"}, {"exercise", "k1"}} {
		out.Reset()
		code, err = checkCommand(&out, checkReport(), c[0], c[1], locateNothing, checkLifetimes)
		require.Equal(t, CheckUndecided, code, c)
		require.True(t, errors.Is(err, ErrCheckUndecided), c)
	}

	out.Reset()
	code, err = checkCommand(&out, rep, "", "", locateNothing, checkLifetimes)
	require.NoError(t, err)
	require.Equal(t, CheckRetirable, code, "the overview is informational")
	require.Contains(t, out.String(), "[platform] k1: not yet")
}

// A derived key prints the rule of its master version, with the configured session lifetimes.
func TestCheckCommand_DerivedKeyPrintsTheMasterRule(t *testing.T) {
	var out bytes.Buffer
	code, err := checkCommand(&out, checkReport(), "vpn", "d-m1", locateNothing, checkLifetimes)
	require.NoError(t, err)
	require.Equal(t, CheckRetirable, code)
	text := out.String()
	require.Contains(t, text, `derived from master key version "m1"`)
	require.Contains(t, text, "seals more than this family")
	require.Contains(t, text, "SESSION_IDLE_TTL=12h0m0s")
	require.Contains(t, text, "SESSION_ABSOLUTE_TTL=168h0m0s")
	require.Contains(t, text, "cannot know when the instances switched")
	require.Contains(t, text, "may leave the servers when every scanned purpose above is retirable AND")
}

// Where the key is: a file of a key directory with its mode, or the variable it comes from; never the content.
func TestKeyLocator(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k1.key"), []byte("placeholder"), 0o400))
	masterDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(masterDir, "m1.key"), []byte("placeholder"), 0o600))

	cfg := &config.Config{}
	cfg.Platform.SecretsKeyDir = dir
	cfg.Keys.SecretsMasterKeyDir = masterDir
	locate := keyLocator(cfg)

	got := locate(secretkeys.FamilyPlatform, "k1")
	require.Contains(t, got, filepath.Join(dir, "k1.key"))
	require.Contains(t, got, "mode 0400")
	require.NotContains(t, got, "placeholder")

	got = locate(secretkeys.FamilyVPN, "k1")
	require.Contains(t, got, "given in the environment (VPN_SECRETS_KEY)")

	got = locate(secretkeys.FamilyPlatform, "d-m1")
	require.Contains(t, got, "goes with the master key version")
	require.Contains(t, got, filepath.Join(masterDir, "m1.key"))
	require.Contains(t, got, "mode 0600")

	got = locate(secretkeys.FamilyPlatform, "k7") // a directory, but no such file
	require.Contains(t, got, "was not found by this process")
	require.Contains(t, got, "PLATFORM_SECRETS_KEY")
}
