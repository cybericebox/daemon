package infrastructure_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	labnames "github.com/cybericebox/laboratory/pkg/names"

	infra "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// Only `<CODE>:<n>/<m>` with a code of the list passes; the free text, node names and paths of an agent never do.
func TestParseUnavailableReason(t *testing.T) {
	assert.Equal(t, &infra.UnavailableReason{Code: "CONTAINERD_ROOT_MISSING", NodesFailed: 2, NodesAsked: 3}, infra.ParseUnavailableReason("CONTAINERD_ROOT_MISSING:2/3"))
	for _, code := range labnames.UnavailableReasonCodes {
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

func TestReasonErrorMapsThePersistenceReason(t *testing.T) {
	assert.ErrorIs(t, infra.ReasonError(infra.ReasonPersistenceUnavailable), infra.ErrDevicePersistenceUnavailable.Err())
}
