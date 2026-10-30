package resourcesModel_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// A microVM node costs its preset plus the overhead the agents report (the largest of them), in the amount and in
// blocks; a container and a platform without reports cost the preset only.
func TestPolicyDevices_AddsTheMicroVMOverhead(t *testing.T) {
	policy := resourcesModel.DefaultPolicy()
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeNode, ResourcePreset: "small"},
		{ID: uuid.Must(uuid.NewV7()), Name: "kernel", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileMicroVM, ResourcePreset: "small"},
	}}
	own := policy.Devices(topology)
	assert.Equal(t, own[0].Amount, own[1].Amount, "no report, no overhead")

	policy.Overheads = resourcesModel.NewProfileOverheads()
	small := resourcesModel.Amount{CPUMillicores: 50, MemoryBytes: 100 << 20}
	large := resourcesModel.Amount{CPUMillicores: 50, MemoryBytes: 120 << 20}
	policy.Overheads.Set(uuid.Must(uuid.NewV7()), map[exerciseModel.NodeProfile]resourcesModel.Amount{exerciseModel.NodeProfileMicroVM: small})
	policy.Overheads.Set(uuid.Must(uuid.NewV7()), map[exerciseModel.NodeProfile]resourcesModel.Amount{exerciseModel.NodeProfileMicroVM: large})

	got := policy.Devices(topology)
	assert.Equal(t, own[0], got[0], "a container costs its preset")
	assert.Equal(t, own[1].Amount.Add(large), got[1].Amount)
	assert.Greater(t, got[1].Blocks, own[1].Blocks)
	assert.Equal(t, got[0].Amount.Add(got[1].Amount), policy.Total(topology).Amount)
}
