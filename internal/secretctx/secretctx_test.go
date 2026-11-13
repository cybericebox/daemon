package secretctx_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/secretctx"
	"github.com/cybericebox/daemon/pkg/secret"
)

func uid(n string) uuid.UUID { return uuid.FromStringOrNil("00000000-0000-4000-8000-" + n) }

// 1. every builder gives, byte for byte, the string the code used before the builders were gathered here. These strings are
// part of what the stored data means: a change makes sealed values unreadable.
func TestBuildersGiveTheStringsTheStoredDataWasSealedWith(t *testing.T) {
	id := uid("000000000001")
	user, ref := uid("000000000004"), uid("000000000005")
	cases := map[string]struct {
		got  []byte
		want string
	}{
		"smtp":            {secretctx.SMTPPassword(id), "mail.smtp:00000000-0000-4000-8000-000000000001"},
		"agent key":       {secretctx.AgentSecret(id, "key"), "infra.agent:00000000-0000-4000-8000-000000000001:key"},
		"agent access":    {secretctx.AgentSecret(id, "access"), "infra.agent:00000000-0000-4000-8000-000000000001:access"},
		"registry push":   {[]byte(secretctx.RegistryPush), "image-registry:push"},
		"registry pull":   {[]byte(secretctx.RegistryPull), "image-registry:pull"},
		"event registry":  {secretctx.EventRegistry(id), "event-registry:00000000-0000-4000-8000-000000000001"},
		"vpn event":       {secretctx.VPNConfig(user, "event", uuid.NullUUID{UUID: ref, Valid: true}), "vpn-config:00000000-0000-4000-8000-000000000004:event:00000000-0000-4000-8000-000000000005"},
		"vpn test":        {secretctx.VPNConfig(user, "test", uuid.NullUUID{}), "vpn-config:00000000-0000-4000-8000-000000000004:test:"},
		"exercise env":    {secretctx.ExerciseEnv(id, "web", "DB_PASS"), "exercise-env:00000000-0000-4000-8000-000000000001:web:DB_PASS"},
		"notify payload":  {secretctx.NotifyPayload(id), "notify-payload:00000000-0000-4000-8000-000000000001"},
		"auth delivery":   {secretctx.AuthDelivery(id), "auth-delivery:00000000-0000-4000-8000-000000000001"},
		"import password": {secretctx.ArchiveImportPassword(id), "exercise-import:password:00000000-0000-4000-8000-000000000001"},
		"export password": {secretctx.ArchiveExportPassword(id), "exercise-export:password:00000000-0000-4000-8000-000000000001"},
		"session ticket":  {secretctx.SessionTicket(), "cybericebox/session-ticket/v1"},
	}
	for name, c := range cases {
		require.Equal(t, c.want, string(c.got), name)
	}
}

type fixtureFile struct {
	GeneratedFrom string `json:"generated_from"`
	Key           string `json:"key"`
	Fixtures      []struct {
		Name       string            `json:"name"`
		Inputs     map[string]string `json:"inputs"`
		Plaintext  string            `json:"plaintext"`
		Ciphertext string            `json:"ciphertext"`
	} `json:"fixtures"`
}

// contextOf rebuilds the context of a fixture from its inputs with the new builders.
func contextOf(t *testing.T, name string, in map[string]string) []byte {
	t.Helper()
	id := func(key string) uuid.UUID { return uuid.FromStringOrNil(in[key]) }
	switch name {
	case "smtp":
		return secretctx.SMTPPassword(id("id"))
	case "agent-key":
		return secretctx.AgentSecret(id("id"), "key")
	case "agent-access":
		return secretctx.AgentSecret(id("id"), "access")
	case "registry-push":
		return []byte(secretctx.RegistryPush)
	case "registry-pull":
		return []byte(secretctx.RegistryPull)
	case "event-registry":
		return secretctx.EventRegistry(id("id"))
	case "vpn-event":
		return secretctx.VPNConfig(id("user"), in["scope"], uuid.NullUUID{UUID: id("ref"), Valid: true})
	case "vpn-test":
		return secretctx.VPNConfig(id("user"), in["scope"], uuid.NullUUID{})
	case "exercise-env":
		return secretctx.ExerciseEnv(id("variant"), in["device"], in["env"])
	case "notify-payload":
		return secretctx.NotifyPayload(id("dispatch"))
	case "auth-delivery":
		return secretctx.AuthDelivery(id("id"))
	case "archive-import-password":
		return secretctx.ArchiveImportPassword(id("job"))
	case "archive-export-password":
		return secretctx.ArchiveExportPassword(id("job"))
	case "session-ticket":
		return secretctx.SessionTicket()
	}
	t.Fatalf("fixture %q has no builder: add it to contextOf", name)
	return nil
}

// 2. compatibility, not self-consistency: the ciphertexts in testdata were sealed by the code of origin/develop before the
// builders were moved (generated once, never regenerated), and they open with the new builders. Nothing stored has to move.
func TestCiphertextsSealedBeforeTheMoveOpenWithTheBuilders(t *testing.T) {
	raw, err := os.ReadFile("testdata/context_fixtures.json")
	require.NoError(t, err)
	var file fixtureFile
	require.NoError(t, json.Unmarshal(raw, &file))
	require.Equal(t, "485211d9", file.GeneratedFrom)
	require.GreaterOrEqual(t, len(file.Fixtures), 14)
	cipher, err := secret.New(file.Key)
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, f := range file.Fixtures {
		seen[f.Name] = true
		plain, openErr := cipher.DecryptWithContext(f.Ciphertext, contextOf(t, f.Name, f.Inputs))
		require.NoError(t, openErr, f.Name)
		require.Equal(t, f.Plaintext, string(plain), f.Name)
	}
	// every builder of the package is covered by a fixture
	for _, name := range []string{"smtp", "agent-key", "agent-access", "registry-push", "registry-pull", "event-registry", "vpn-event",
		"vpn-test", "exercise-env", "notify-payload", "auth-delivery", "archive-import-password", "archive-export-password", "session-ticket"} {
		require.True(t, seen[name], "no fixture for %s", name)
	}
}

// 6. different purposes never produce equal contexts for the same inputs, and a ciphertext of one does not open as another.
func TestContextsOfDifferentPurposesDiffer(t *testing.T) {
	id := uid("000000000001")
	all := map[string][]byte{
		"smtp": secretctx.SMTPPassword(id), "agent key": secretctx.AgentSecret(id, "key"), "agent access": secretctx.AgentSecret(id, "access"),
		"registry push": []byte(secretctx.RegistryPush), "registry pull": []byte(secretctx.RegistryPull), "event registry": secretctx.EventRegistry(id),
		"vpn": secretctx.VPNConfig(id, "event", uuid.NullUUID{UUID: id, Valid: true}), "env": secretctx.ExerciseEnv(id, "web", "A"),
		"notify": secretctx.NotifyPayload(id), "auth": secretctx.AuthDelivery(id), "import": secretctx.ArchiveImportPassword(id),
		"export": secretctx.ArchiveExportPassword(id), "ticket": secretctx.SessionTicket(),
	}
	seen := map[string]string{}
	for name, ctx := range all {
		require.NotEmpty(t, ctx, name)
		if other, dup := seen[string(ctx)]; dup {
			t.Fatalf("%s and %s have the same context", name, other)
		}
		seen[string(ctx)] = name
	}
	cipher, err := secret.New("k1:" + strings.Repeat("a1", 32))
	require.NoError(t, err)
	sealed, err := cipher.EncryptWithContext([]byte("x"), all["smtp"])
	require.NoError(t, err)
	for name, ctx := range all {
		_, openErr := cipher.DecryptWithContext(sealed, ctx)
		if name == "smtp" {
			require.NoError(t, openErr)
		} else {
			require.Error(t, openErr, "a value sealed for smtp must not open as %s", name)
		}
	}
}

// 6. the known ambiguity of the existing form: the parts of an env context are joined with ':' and free text, so two names can
// run into one another. This is why a device or env name with ':' is refused when a draft is saved (see exerciseModel.
// ValidateDeviceNames); the stored strings cannot change.
func TestExerciseEnvContextIsAmbiguousOnlyWhenANameHoldsTheSeparator(t *testing.T) {
	v := uid("000000000006")
	require.Equal(t, string(secretctx.ExerciseEnv(v, "a:b", "c")), string(secretctx.ExerciseEnv(v, "a", "b:c")))
	require.NotEqual(t, string(secretctx.ExerciseEnv(v, "ab", "c")), string(secretctx.ExerciseEnv(v, "a", "bc")))
}
