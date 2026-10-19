package eventLabAllocationRepo

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

func TestInitialGroupAllocationKeepsGroupPodsGuaranteed(t *testing.T) {
	g := Group{Sizes: infraModel.GroupSizes{
		VPN:     resourcesModel.Amount{CPUMillicores: 15, MemoryBytes: 64 << 20},
		Gateway: resourcesModel.Amount{CPUMillicores: 250, MemoryBytes: 1 << 30},
	}}
	var a eventLabModel.Allocation
	require.NoError(t, json.Unmarshal(initialGroupAllocation(g), &a))
	assert.EqualValues(t, 265, a.ConfiguredRequests.CPUMillicores, "requests are the plain sum")
	assert.EqualValues(t, 265, a.AllocatedRequests.CPUMillicores)
	assert.EqualValues(t, 265, a.ConfiguredLimits.CPUMillicores, "VPN and gateway pods do not burst: limit equals request")
	assert.EqualValues(t, (64<<20)+(1<<30), a.ConfiguredLimits.MemoryBytes, "memory limit equals request")
}
