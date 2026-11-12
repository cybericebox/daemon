package secretkeys

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	vpnUseCase "github.com/cybericebox/daemon/internal/useCase/vpn"
	"github.com/cybericebox/daemon/pkg/secret"
)

const (
	hexOld = "000102030405060708090a0b0c0d0e0f000102030405060708090a0b0c0d0e0f"
	hexNew = "101112131415161718191a1b1c1d1e1f101112131415161718191a1b1c1d1e1f"
	hexLst = "202122232425262728292a2b2c2d2e2f202122232425262728292a2b2c2d2e2f"
)

var (
	idSMTP  = uuid.FromStringOrNil("11111111-1111-7111-8111-111111111111")
	idAgent = uuid.FromStringOrNil("22222222-2222-7222-8222-222222222222")
	idEvent = uuid.FromStringOrNil("33333333-3333-7333-8333-333333333333")
	idUser  = uuid.FromStringOrNil("44444444-4444-7444-8444-444444444444")
	idRef   = uuid.FromStringOrNil("55555555-5555-7555-8555-555555555555")
	idVer   = uuid.FromStringOrNil("66666666-6666-7666-8666-666666666666")
	idVar   = uuid.FromStringOrNil("77777777-7777-7777-8777-777777777777")
)

func ring(t *testing.T, spec string) *secret.Cipher {
	t.Helper()
	c, err := secret.New(spec)
	require.NoError(t, err)
	return c
}

func seal(t *testing.T, c *secret.Cipher, plain string, context []byte) string {
	t.Helper()
	out, err := c.EncryptWithContext([]byte(plain), context)
	require.NoError(t, err)
	return out
}

// fakeStore holds what the tables would hold.
type fakeStore struct {
	smtp                  string
	agentKey, agentAccess string
	push, pull            string
	eventToken            string
	vpn                   string
	variants              []exerciseModel.Variant
}

func (s *fakeStore) SMTPPasswords(_ context.Context, visit func(uuid.UUID, string) error) error {
	return visit(idSMTP, s.smtp)
}
func (s *fakeStore) AgentKeys(_ context.Context, visit func(uuid.UUID, string, string) error) error {
	return visit(idAgent, s.agentKey, s.agentAccess)
}
func (s *fakeStore) RegistrySettings(_ context.Context, visit func(string, string) error) error {
	return visit(s.push, s.pull)
}
func (s *fakeStore) EventRegistryTokens(_ context.Context, visit func(uuid.UUID, string) error) error {
	return visit(idEvent, s.eventToken)
}
func (s *fakeStore) VPNConfigs(_ context.Context, visit func(uuid.UUID, string, uuid.NullUUID, string) error) error {
	return visit(idUser, string(vpnModel.ScopeEvent), uuid.NullUUID{UUID: idRef, Valid: true}, s.vpn)
}
func (s *fakeStore) ExerciseVersions(_ context.Context, visit func(uuid.UUID, []byte) error) error {
	raw, err := json.Marshal(s.variants)
	if err != nil {
		return err
	}
	return visit(idVer, raw)
}

func exerciseVariant(sealed string) []exerciseModel.Variant {
	return []exerciseModel.Variant{{ID: idVar, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
		Name: "web", Bootstrap: exerciseModel.Bootstrap{Env: []exerciseModel.EnvVar{
			{Name: "PLAIN", Value: "visible"},
			{Name: "DB_PASS", Value: sealed, Secret: true},
		}},
	}}}}}
}

// sealedBy seals every source with the given rings.
func sealedBy(t *testing.T, platform, vpn, exercise *secret.Cipher) *fakeStore {
	t.Helper()
	return &fakeStore{
		smtp:        seal(t, platform, "smtp", mailUseCase.PasswordContext(idSMTP)),
		agentKey:    seal(t, platform, "agent", infraModel.AgentSecretContext(idAgent, "key")),
		agentAccess: seal(t, platform, "access", infraModel.AgentSecretContext(idAgent, "access")),
		push:        seal(t, platform, "push", []byte(imageRegistryUseCase.PushContext)),
		pull:        "",
		eventToken:  seal(t, platform, "event", []byte(imageRegistryUseCase.EventSecretContext(idEvent))),
		vpn:         seal(t, vpn, "wg", vpnUseCase.ConfigContext(idUser, vpnModel.ScopeEvent, uuid.NullUUID{UUID: idRef, Valid: true})),
		variants:    exerciseVariant(seal(t, exercise, "pw", exerciseUseCase.EnvSecretContext(idVar, "web", "DB_PASS"))),
	}
}

func column(t *testing.T, rep Report, name string) Column {
	t.Helper()
	for _, c := range rep.Columns {
		if c.Name == name {
			return c
		}
	}
	require.FailNow(t, "no such column in the report", name)
	return Column{}
}

func TestStatusCountsValuesPerKeyAndFindsNothingWrongInAHealthyState(t *testing.T) {
	oldRing := ring(t, "k1:"+hexOld)
	both := ring(t, "k2:"+hexNew+",k1:"+hexOld)
	store := sealedBy(t, oldRing, both, both)
	store.smtp = seal(t, both, "smtp", mailUseCase.PasswordContext(idSMTP)) // sealed after the switch
	uc := New(store, Ciphers{Platform: both, VPN: both, Exercise: both})

	rep, err := uc.Check(context.Background(), true)
	require.NoError(t, err)
	require.Zero(t, rep.Problems())
	require.Equal(t, []string{"k2", "k1"}, rep.Keys[FamilyPlatform])
	require.Equal(t, map[string]int{"k2": 1}, column(t, rep, "mail_smtp_configs.password_ciphertext").ByKey)
	require.Equal(t, map[string]int{"k1": 1}, column(t, rep, "infrastructure_agents.client_key_ciphertext").ByKey)
	require.Equal(t, map[string]int{"k2": 1}, column(t, rep, "user_vpn_configs.config").ByKey)
	secrets := column(t, rep, "exercise_versions.variants@topology.devices[].bootstrap.env[]")
	require.Equal(t, 1, secrets.Total, "only the secret env var is counted")
	require.Equal(t, 1, column(t, rep, "image_registry_settings.push_token_ciphertext").Total)
	require.NotContains(t, columnNames(rep), "image_registry_settings.pull_token_ciphertext", "an empty value is not a value")
}

func columnNames(rep Report) []string {
	var out []string
	for _, c := range rep.Columns {
		out = append(out, c.Name)
	}
	return out
}

// A key that was dropped from the ring while values still name it is found by the cheap check and by the full one, with the
// row and the missing id, without opening anything.
func TestAMissingKeyIsFoundByIdWithoutOpeningAnything(t *testing.T) {
	lost := ring(t, "k0:"+hexLst)
	current := ring(t, "k2:"+hexNew)
	store := sealedBy(t, current, current, current)
	store.eventToken = seal(t, lost, "event", []byte(imageRegistryUseCase.EventSecretContext(idEvent)))
	uc := New(store, Ciphers{Platform: current, VPN: current, Exercise: current})

	for _, open := range []bool{false, true} {
		rep, err := uc.Check(context.Background(), open)
		require.NoError(t, err)
		require.Equal(t, 1, rep.Problems(), "open=%v", open)
		col := column(t, rep, "event_registry_secrets.token_ciphertext")
		require.Equal(t, []Problem{{Kind: KeyNotLoaded, KeyID: "k0", Ref: idEvent.String()}}, col.ProblemRefs)
	}
}

// What the cheap check cannot see (a value that names a loaded key but does not open: damaged, or a row context that
// changed) the full check reports.
func TestAValueThatDoesNotOpenIsOnlyFoundByTheFullCheck(t *testing.T) {
	current := ring(t, "k2:"+hexNew)
	store := sealedBy(t, current, current, current)
	store.smtp = seal(t, current, "smtp", []byte("mail.smtp:another-row"))
	uc := New(store, Ciphers{Platform: current, VPN: current, Exercise: current})

	cheap, err := uc.Check(context.Background(), false)
	require.NoError(t, err)
	require.Zero(t, cheap.Problems())
	full, err := uc.Check(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 1, full.Problems())
	require.Equal(t, []Problem{{Kind: CannotOpen, KeyID: "k2", Ref: idSMTP.String()}}, column(t, full, "mail_smtp_configs.password_ciphertext").ProblemRefs)
}

// The older form without a key id is counted; it is a problem only when no loaded key opens it.
func TestLegacyValuesAreCountedAndOpenedByTrial(t *testing.T) {
	old := ring(t, "k1:"+hexOld)
	sealed := seal(t, old, "smtp", mailUseCase.PasswordContext(idSMTP))
	legacy := strings.TrimPrefix(sealed, "v1:k1:")
	require.NotEqual(t, sealed, legacy)
	ciphers := ring(t, "k2:"+hexNew+",k1:"+hexOld)

	store := sealedBy(t, ciphers, ciphers, ciphers)
	store.smtp = legacy
	rep, err := New(store, Ciphers{Platform: ciphers, VPN: ciphers, Exercise: ciphers}).Check(context.Background(), true)
	require.NoError(t, err)
	require.Zero(t, rep.Problems())
	smtp := column(t, rep, "mail_smtp_configs.password_ciphertext")
	require.Equal(t, 1, smtp.Legacy)
	require.Empty(t, smtp.ByKey)

	// the same value when the key that sealed it is gone
	gone := ring(t, "k2:"+hexNew)
	store = sealedBy(t, gone, gone, gone)
	store.smtp = legacy
	for open, want := range map[bool]int{false: 0, true: 1} {
		rep, err = New(store, Ciphers{Platform: gone, VPN: gone, Exercise: gone}).Check(context.Background(), open)
		require.NoError(t, err)
		require.Equal(t, want, rep.Problems(), "open=%v", open)
	}
}

func TestAFamilyWithoutAKeyReportsItsValues(t *testing.T) {
	current := ring(t, "k2:"+hexNew)
	store := sealedBy(t, current, current, current)
	rep, err := New(store, Ciphers{Platform: current, VPN: nil, Exercise: current}).Check(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Problems())
	require.Equal(t, NoFamilyKey, column(t, rep, "user_vpn_configs.config").ProblemRefs[0].Kind)
	require.Nil(t, rep.Keys[FamilyVPN])
}

func TestTheReportNamesRowsAndKeysNeverValues(t *testing.T) {
	lost := ring(t, "k0:"+hexLst)
	current := ring(t, "k2:"+hexNew)
	store := sealedBy(t, current, current, current)
	store.vpn = seal(t, lost, "PRIVATE-WG-KEY", vpnUseCase.ConfigContext(idUser, vpnModel.ScopeEvent, uuid.NullUUID{UUID: idRef, Valid: true}))
	rep, err := New(store, Ciphers{Platform: current, VPN: current, Exercise: current}).Check(context.Background(), true)
	require.NoError(t, err)
	var out bytes.Buffer
	rep.Write(&out)
	text := out.String()
	require.Contains(t, text, `sealed by key "k0", which is not loaded`)
	require.Contains(t, text, "RESULT: 1 value(s) cannot be opened")
	require.Contains(t, text, idUser.String())
	require.NotContains(t, text, store.vpn, "the ciphertext is never printed")
	require.NotContains(t, text, "PRIVATE-WG-KEY")
	require.NotContains(t, text, hexNew)
}
