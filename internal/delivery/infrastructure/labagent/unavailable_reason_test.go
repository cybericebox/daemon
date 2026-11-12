package labagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// The reasons an agent gives for a feature it holds back reach the report, through the generated types, for the
// persistence and the lifecycle features; only a value of the fixed grammar and code list passes.
func TestFeaturesOfCarriesTheUnavailableReasons(t *testing.T) {
	features := FeaturesOf(&labpb.FeaturesResponse{
		StatePersistence: &labpb.StatePersistenceFeature{UnavailableReason: "NODE_UNCONFIRMED:2/3"},
		Lifecycle:        &labpb.LifecycleFeature{UnavailableReason: "CGROUP_V2_MISSING:1/4"},
	})
	assert.Equal(t, &infraModel.UnavailableReason{Code: "NODE_UNCONFIRMED", NodesFailed: 2, NodesAsked: 3}, features.PersistenceUnavailable)
	assert.Equal(t, &infraModel.UnavailableReason{Code: "CGROUP_V2_MISSING", NodesFailed: 1, NodesAsked: 4}, features.LifecycleUnavailable)

	none := FeaturesOf(&labpb.FeaturesResponse{})
	assert.Nil(t, none.PersistenceUnavailable, "empty: nothing is held back")
	assert.Nil(t, none.LifecycleUnavailable)

	dropped := FeaturesOf(&labpb.FeaturesResponse{StatePersistence: &labpb.StatePersistenceFeature{UnavailableReason: "node kind-worker: no cgroup tree"}})
	assert.Nil(t, dropped.PersistenceUnavailable, "free text is dropped")
	require.NotNil(t, features.PersistenceUnavailable)
}
