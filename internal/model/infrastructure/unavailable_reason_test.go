package infrastructure_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infra "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// Only `<CODE>:<n>/<m>` with a code of the list passes; the free text, node names and paths of an agent never do.
func TestParseUnavailableReason(t *testing.T) {
	assert.Equal(t, &infra.UnavailableReason{Code: "CONTAINERD_ROOT_MISSING", NodesFailed: 2, NodesAsked: 3}, infra.ParseUnavailableReason("CONTAINERD_ROOT_MISSING:2/3"))
	for _, code := range infra.UnavailableCodes {
		assert.NotNil(t, infra.ParseUnavailableReason(code+":0/1"), code)
	}
	for _, bad := range []string{
		"", "CONTAINERD_ROOT_MISSING", "CONTAINERD_ROOT_MISSING:", "CONTAINERD_ROOT_MISSING:2", "CONTAINERD_ROOT_MISSING:2/",
		"containerd_root_missing:2/3", "UNKNOWN_CODE:1/2", "CONTAINERD_ROOT_MISSING:3/2", "CONTAINERD_ROOT_MISSING:1/0",
		"CONTAINERD_ROOT_MISSING:-1/2", "CONTAINERD_ROOT_MISSING:+1/2", "CONTAINERD_ROOT_MISSING:1/2 node kind-worker",
		"CONTAINERD_ROOT_MISSING:1/2\n", "state persistence: node kind-worker: /var/lib/k0s/containerd is missing",
		"CONTAINERD_ROOT_MISSING:1/" + strings.Repeat("9", 20), "CONTAINERD_ROOT_MISSING:٣/٤",
	} {
		assert.Nil(t, infra.ParseUnavailableReason(bad), bad)
	}
}

// The allow-list is the laboratory's (pkg/names.UnavailableReasonCodes): compared when its source is next to the
// daemon; once the pinned release has the package, import it.
func TestUnavailableCodesMatchTheLaboratory(t *testing.T) {
	dir, err := os.Getwd()
	require.NoError(t, err)
	for ; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		var raw []byte
		for _, candidate := range []string{
			filepath.Join(dir, "laboratory", "pkg", "names", "unavailable.go"),
			filepath.Join(dir, "laboratory", ".worktrees", "hardening", "pkg", "names", "unavailable.go"),
		} {
			if content, readErr := os.ReadFile(candidate); readErr == nil {
				raw = content
				break
			}
		}
		if raw == nil {
			continue
		}
		var codes []string
		for _, m := range regexp.MustCompile(`Reason\w+\s*=\s*"([A-Z0-9_]+)"`).FindAllSubmatch(raw, -1) {
			codes = append(codes, string(m[1]))
		}
		assert.ElementsMatch(t, codes, infra.UnavailableCodes)
		return
	}
	t.Skip("the laboratory source is not next to the daemon")
}

func TestReasonErrorMapsThePersistenceReason(t *testing.T) {
	assert.ErrorIs(t, infra.ReasonError(infra.ReasonPersistenceUnavailable), infra.ErrDevicePersistenceUnavailable.Err())
}
