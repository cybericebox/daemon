package event

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// A lab made before node profiles stored the hash of a topology with the type container. The same device as a node
// with the container profile (what migration 0207 writes) must hash the same, or every such lab would look changed.
func TestDefinitionHash_ContainerNodeHashesAsTheFormerContainer(t *testing.T) {
	version := uuid.Must(uuid.NewV7())
	former := exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx:1"}}}
	migrated := exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileContainer, Image: "nginx:1"}}}
	assert.Equal(t, definitionHash(version, 1, former), definitionHash(version, 1, migrated))

	microvm := exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileMicroVM, Image: "nginx:1"}}}
	assert.NotEqual(t, definitionHash(version, 1, former), definitionHash(version, 1, microvm))
	assert.Equal(t, exerciseModel.NodeProfileContainer, migrated.Devices[0].Profile, "the caller's topology is not changed")
}
