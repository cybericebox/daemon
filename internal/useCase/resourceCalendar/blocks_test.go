package resourceCalendarUseCase

import (
	"testing"

	"github.com/stretchr/testify/assert"

	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// A device is a whole number of blocks, so the largest device an agent can place is mapped down to the largest
// allowed block: a device above it does not fit that agent.
func TestLargestPlaceableDeviceIsMappedDownToTheLargestAllowedBlock(t *testing.T) {
	u := &ResourceCalendarUseCase{policy: resourcesModel.DefaultPolicy()}
	const mi, gi = 1 << 20, 1 << 30
	for name, tc := range map[string]struct{ limit, want Amount }{
		"no limit stays unlimited":          {Amount{}, Amount{}},
		"between two blocks goes down":      {Amount{CPUMillicores: 3000, MemoryBytes: 3 * gi}, Amount{CPUMillicores: 500, MemoryBytes: 2 * gi}},
		"memory limits the block":           {Amount{CPUMillicores: 8000, MemoryBytes: 600 * mi}, Amount{CPUMillicores: 125, MemoryBytes: 512 * mi}},
		"cpu limits the block":              {Amount{CPUMillicores: 40, MemoryBytes: 8 * gi}, Amount{CPUMillicores: 32, MemoryBytes: 128 * mi}},
		"above the ceiling is the ceiling":  {Amount{CPUMillicores: 16000, MemoryBytes: 64 * gi}, Amount{CPUMillicores: 1000, MemoryBytes: 4 * gi}},
		"an unlimited cpu is not a limit":   {Amount{MemoryBytes: gi}, Amount{CPUMillicores: 250, MemoryBytes: gi}},
		"below the smallest block is as is": {Amount{CPUMillicores: 8, MemoryBytes: 32 * mi}, Amount{CPUMillicores: 8, MemoryBytes: 32 * mi}},
	} {
		assert.Equal(t, tc.want, u.largestBlock(tc.limit), name)
	}
}
