package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	hexA = "000102030405060708090a0b0c0d0e0f000102030405060708090a0b0c0d0e0f"
	hexB = "101112131415161718191a1b1c1d1e1f101112131415161718191a1b1c1d1e1f"
)

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o400))
	}
	return dir
}

func TestSpecFromDirBuildsARingWithTheCurrentKeyFirst(t *testing.T) {
	dir := writeDir(t, map[string]string{"k1.key": hexA + "\n", "k2.key": hexB, "CURRENT": "k2\n"})
	spec, err := SpecFromDir(dir)
	require.NoError(t, err)
	require.Equal(t, "k2:"+hexB+",k1:"+hexA, spec)
	c, err := New(spec)
	require.NoError(t, err)
	require.Equal(t, []string{"k2", "k1"}, c.KeyIDs())
	require.Equal(t, "k2", c.CurrentKeyID())

	// a value sealed before the switch still opens
	old, err := New("k1:" + hexA)
	require.NoError(t, err)
	sealed, err := old.EncryptWithContext([]byte("x"), []byte("ctx"))
	require.NoError(t, err)
	plain, err := c.DecryptWithContext(sealed, []byte("ctx"))
	require.NoError(t, err)
	require.Equal(t, "x", string(plain))
}

func TestSpecFromDirIgnoresDotNamesOfAMountedSecret(t *testing.T) {
	dir := writeDir(t, map[string]string{"k1.key": hexA, "CURRENT": "k1", "..data_tmp": "junk"})
	require.NoError(t, os.Mkdir(filepath.Join(dir, "..2026_11_13"), 0o700))
	_, err := SpecFromDir(dir)
	require.NoError(t, err)
}

func TestSpecFromDirRefusesWhatIsAmbiguousOrUnsafe(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		mode  os.FileMode
		want  string
	}{
		"no current":      {map[string]string{"k1.key": hexA}, 0o400, "no CURRENT"},
		"current unknown": {map[string]string{"k1.key": hexA, "CURRENT": "k9"}, 0o400, `names the key "k9"`},
		"no key":          {map[string]string{"CURRENT": "k1"}, 0o400, "no <id>.key"},
		"stray file":      {map[string]string{"k1.key": hexA, "CURRENT": "k1", "notes.txt": "x"}, 0o400, "not expected"},
		"bad id":          {map[string]string{"K1.key": hexA, "CURRENT": "K1"}, 0o400, "a key id is"},
		"short key":       {map[string]string{"k1.key": "abcd", "CURRENT": "k1"}, 0o400, "k1.key"},
		"group readable":  {map[string]string{"k1.key": hexA, "CURRENT": "k1"}, 0o440, "readable by its group"},
		"world readable":  {map[string]string{"k1.key": hexA, "CURRENT": "k1"}, 0o644, "readable by its group or by everybody"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, content := range tc.files {
				mode := os.FileMode(0o400)
				if strings.HasSuffix(file, ".key") {
					mode = tc.mode
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(content), mode))
				require.NoError(t, os.Chmod(filepath.Join(dir, file), mode))
			}
			_, err := SpecFromDir(dir)
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), hexA, "no key material in a message")
		})
	}
	_, err := SpecFromDir(filepath.Join(t.TempDir(), "missing"))
	require.ErrorContains(t, err, "cannot be read")
	_, err = SpecFromDir(t.TempDir())
	require.ErrorContains(t, err, "no <id>.key")
	nested := writeDir(t, map[string]string{"k1.key": hexA, "CURRENT": "k1"})
	require.NoError(t, os.Mkdir(filepath.Join(nested, "nested"), 0o700))
	_, err = SpecFromDir(nested)
	require.ErrorContains(t, err, "not a regular file")
}

func TestSpecFromDirFollowsLinks(t *testing.T) {
	data := writeDir(t, map[string]string{"k1.key": hexA, "CURRENT": "k1"})
	dir := t.TempDir()
	for _, name := range []string{"k1.key", "CURRENT"} {
		require.NoError(t, os.Symlink(filepath.Join(data, name), filepath.Join(dir, name)))
	}
	_, err := SpecFromDir(dir)
	require.NoError(t, err)
}

func TestKeyIDAndHasKey(t *testing.T) {
	c, err := New("k2:" + hexB + ",k1:" + hexA)
	require.NoError(t, err)
	sealed, err := c.Encrypt("v")
	require.NoError(t, err)
	id, legacy := KeyID(sealed)
	require.Equal(t, "k2", id)
	require.False(t, legacy)
	_, legacy = KeyID("AAAA")
	require.True(t, legacy)
	require.True(t, c.HasKey("k1"))
	require.False(t, c.HasKey("k3"))
}
