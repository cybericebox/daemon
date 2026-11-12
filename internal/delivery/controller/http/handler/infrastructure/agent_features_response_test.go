package infrastructure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// The agent list carries what the agent confirms for stop and restart, and the held-back reasons as a code and counts only.
func TestToFeaturesResponse_LifecycleAndReasons(t *testing.T) {
	out := toFeaturesResponse(&infraModel.AgentFeatures{
		Lifecycle:              infraModel.LifecycleCapabilities{PerLabStop: true, ConfirmedRuntime: true},
		PersistenceUnavailable: &infraModel.UnavailableReason{Code: "CONTAINERD_ROOT_MISSING", NodesFailed: 1, NodesAsked: 2},
	})
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, map[string]any{"PerLabStop": true, "RequiredSnapshot": false, "ConfirmedRuntime": true, "RetainedRestart": false, "FullGroupStop": false}, got["Lifecycle"])
	assert.Equal(t, map[string]any{"Code": "CONTAINERD_ROOT_MISSING", "NodesFailed": float64(1), "NodesAsked": float64(2)}, got["PersistenceUnavailable"])
	assert.Nil(t, got["LifecycleUnavailable"])
}
