package event

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The hash is taken from the topology as it is stored: a container node and a microVM node differ, the same
// topology hashes the same, and so do the version and the generation.
func TestDefinitionHash_FollowsTheNodeProfile(t *testing.T) {
	version := uuid.Must(uuid.NewV7())
	node := func(profile exerciseModel.NodeProfile) exerciseModel.Topology {
		return exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: profile, Image: "nginx:1"}}}
	}
	container, microvm := node(exerciseModel.NodeProfileContainer), node(exerciseModel.NodeProfileMicroVM)

	assert.Equal(t, definitionHash(version, 1, container), definitionHash(version, 1, node(exerciseModel.NodeProfileContainer)))
	assert.NotEqual(t, definitionHash(version, 1, container), definitionHash(version, 1, microvm))
	assert.NotEqual(t, definitionHash(version, 1, container), definitionHash(version, 2, container))
	assert.NotEqual(t, definitionHash(version, 1, container), definitionHash(uuid.Must(uuid.NewV7()), 1, container))
}
