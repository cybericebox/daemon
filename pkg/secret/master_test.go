package secret

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Derivations computed with an independent HKDF-SHA256 (RFC 5869: salt none, one output block) from hexA, so a change of the
// construction or of a label, which would make stored data unreadable, fails here.
var goldenDerived = map[Purpose]string{
	PurposePlatform: "0edcc5eae513625d9a556f8274c2f049953e1c2a685a5365602704f646da9caa",
	PurposeVPN:      "8bc3aa48d2de5b51244d52675a593e655985ae30813cf5c6fa7ee92429244b0a",
	PurposeExercise: "96a883cd5f04de973eeca2e8cb31095019d10a900ba4cbd0fa6bab38a244e77e",
	PurposeSession:  "99de726af2e4bc44b44761eefee891bfe29ad0d0ad77f67d6a0052a77bc4b31d",
}

func TestDerivationIsStableAndMatchesAnIndependentHKDF(t *testing.T) {
	for purpose, want := range goldenDerived {
		spec, err := Resolve("m1:"+hexA, "", purpose, false)
		require.NoError(t, err)
		require.Equal(t, "d-m1:"+want, spec, purpose)
		again, err := Resolve("m1:"+hexA, "", purpose, false)
		require.NoError(t, err)
		require.Equal(t, spec, again, "the same master gives the same key after a restart")
	}
	require.Equal(t, "cybericebox/secrets/v1/platform", KDFLabel(PurposePlatform))
	require.Equal(t, "cybericebox/secrets/v1/session", KDFLabel(PurposeSession))
}

func TestPurposesGetDifferentKeysAndDoNotOpenEachOther(t *testing.T) {
	ciphers := map[Purpose]*Cipher{}
	seen := map[string]Purpose{}
	for purpose := range goldenDerived {
		spec, err := Resolve("m1:"+hexA, "", purpose, false)
		require.NoError(t, err)
		_, key, _ := strings.Cut(spec, ":")
		require.NotEqual(t, hexA, key, "a derived key is never the master")
		if other, dup := seen[key]; dup {
			require.Failf(t, "two purposes share a key", "%s and %s", purpose, other)
		}
		seen[key] = purpose
		ciphers[purpose] = ring(t, spec)
	}
	sealed, err := ciphers[PurposeVPN].EncryptWithContext([]byte("wg"), []byte("ctx"))
	require.NoError(t, err)
	for purpose, c := range ciphers {
		plain, err := c.DecryptWithContext(sealed, []byte("ctx"))
		if purpose == PurposeVPN {
			require.NoError(t, err)
			require.Equal(t, "wg", string(plain))
			continue
		}
		require.Error(t, err, "the %s key must not open a vpn value", purpose)
	}
	// A different master gives different keys.
	other, err := Resolve("m1:"+hexB, "", PurposeVPN, false)
	require.NoError(t, err)
	require.NotEqual(t, "d-m1:"+goldenDerived[PurposeVPN], other)
}

func ring(t *testing.T, spec string) *Cipher {
	t.Helper()
	c, err := New(spec)
	require.NoError(t, err)
	return c
}

func TestDerivedIDsSayDerivedAndTheMasterVersion(t *testing.T) {
	spec, err := Resolve("m2:"+hexB+",m1:"+hexA, "", PurposePlatform, false)
	require.NoError(t, err)
	c := ring(t, spec)
	require.Equal(t, []string{"d-m2", "d-m1"}, c.KeyIDs())
	require.Equal(t, "d-m2", c.CurrentKeyID(), "the current master version seals")
	sealed, err := c.Encrypt("x")
	require.NoError(t, err)
	id, _ := KeyID(sealed)
	master, derived := IsDerivedID(id)
	require.True(t, derived)
	require.Equal(t, "m2", master)
	_, derived = IsDerivedID("k1")
	require.False(t, derived)
	_, derived = IsDerivedID("d-")
	require.False(t, derived)
}

// A deployment that already holds data sealed with the separate keys keeps opening it; the derived key seals from now on.
func TestOldExplicitKeysStillOpenOldCiphertext(t *testing.T) {
	old := ring(t, hexB) // the single plain key of the older configuration: id "default"
	oldValue, err := old.EncryptWithContext([]byte("before the master key"), []byte("ctx"))
	require.NoError(t, err)
	oldLegacy := strings.TrimPrefix(oldValue, "v1:default:") // and the form from before key ids

	spec, err := Resolve("m1:"+hexA, hexB, PurposePlatform, false)
	require.NoError(t, err)
	c := ring(t, spec)
	require.Equal(t, []string{"d-m1", "default"}, c.KeyIDs())
	require.Equal(t, "d-m1", c.CurrentKeyID(), "new values are sealed with the derived key")

	for _, value := range []string{oldValue, oldLegacy} {
		plain, err := c.DecryptWithContext(value, []byte("ctx"))
		require.NoError(t, err)
		require.Equal(t, "before the master key", string(plain))
		require.True(t, c.NeedsRotation(value), "re-encryption has to move it to the derived key")
	}
	fresh, err := c.EncryptWithContext([]byte("after"), []byte("ctx"))
	require.NoError(t, err)
	require.False(t, c.NeedsRotation(fresh))
	_, err = old.DecryptWithContext(fresh, []byte("ctx"))
	require.Error(t, err, "the old key does not open what the derived key sealed")
}

func TestAMixedRingOpensEveryGeneration(t *testing.T) {
	explicit := "k1:" + hexB + ",k0:" + hexA // an old keyring of the purpose (not the master)
	first := ring(t, explicit)
	v0, err := first.Encrypt("generation 0")
	require.NoError(t, err)

	masterOne, err := Resolve("m1:"+hexA, explicit, PurposeVPN, false)
	require.NoError(t, err)
	gen1 := ring(t, masterOne)
	v1, err := gen1.Encrypt("generation 1")
	require.NoError(t, err)

	masterTwo, err := Resolve("m2:"+hexB+",m1:"+hexA, explicit, PurposeVPN, false)
	require.NoError(t, err)
	gen2 := ring(t, masterTwo)
	require.Equal(t, []string{"d-m2", "d-m1", "k1", "k0"}, gen2.KeyIDs())
	v2, err := gen2.Encrypt("generation 2")
	require.NoError(t, err)

	for want, value := range map[string]string{"generation 0": v0, "generation 1": v1, "generation 2": v2} {
		got, err := gen2.Decrypt(value)
		require.NoError(t, err, want)
		require.Equal(t, want, got)
	}
	// dropping the m1 master version from the ring makes its values unreadable: the lost key the guard reports
	onlyTwo, err := Resolve("m2:"+hexB, explicit, PurposeVPN, false)
	require.NoError(t, err)
	_, err = ring(t, onlyTwo).Decrypt(v1)
	require.ErrorContains(t, err, `"d-m1"`)
}

func TestAnExplicitKeyMayStaySealing(t *testing.T) {
	spec, err := Resolve("m1:"+hexA, "own:"+hexB, PurposeExercise, true)
	require.NoError(t, err)
	c := ring(t, spec)
	require.Equal(t, []string{"own", "d-m1"}, c.KeyIDs())
	require.Equal(t, "own", c.CurrentKeyID())

	_, err = Resolve("m1:"+hexA, "", PurposeExercise, true)
	require.ErrorContains(t, err, "has none")
	_, err = Resolve("", hexB, PurposeExercise, true)
	require.ErrorContains(t, err, "master key")
}

func TestWithoutAMasterTheOlderConfigurationIsUnchanged(t *testing.T) {
	got, err := Resolve("", hexB, PurposeVPN, false)
	require.NoError(t, err)
	require.Equal(t, hexB, got)
	got, err = Resolve("  ", "", PurposeVPN, false)
	require.NoError(t, err)
	require.Empty(t, got, "no key at all: the feature stays off")
}

func TestResolveRefusesWhatIsAmbiguous(t *testing.T) {
	_, err := Resolve("d-m1:"+hexA, "", PurposeVPN, false)
	require.NoError(t, err, "a master id is free text; the derived id is d- plus it")
	_, err = Resolve("m1:"+hexA, "d-m1:"+hexB, PurposeVPN, false)
	require.ErrorContains(t, err, "reserved")
	_, err = Resolve("m1:"+hexA, "m1:"+hexB, PurposeVPN, false)
	require.NoError(t, err, "an explicit id is not a derived id, even when the master has the same name")
	_, err = Resolve("fifteen-chars-x:"+hexA, "", PurposeVPN, false)
	require.ErrorContains(t, err, "longer than")
	_, err = Resolve("abcd", "", PurposeVPN, false)
	require.ErrorContains(t, err, "master key")
	_, err = Resolve("m1:"+hexA, "k1:"+hexB+",k1:"+hexA, PurposeVPN, false)
	require.Error(t, err)
}
