package infrastructure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

const mi, gi = 1 << 20, 1 << 30

func TestGroupPodSizingGrowsWithUnitsUpToTheMaximum(t *testing.T) {
	vpn := GroupPodSizing{
		Base:    resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 16 * mi},
		PerUnit: resourcesModel.Amount{CPUMillicores: 2, MemoryBytes: 4 * mi},
		Max:     resourcesModel.Amount{CPUMillicores: 50, MemoryBytes: 64 * mi},
	}
	assert.True(t, vpn.Reported())
	assert.Equal(t, resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 16 * mi}, vpn.Size(0))
	assert.Equal(t, resourcesModel.Amount{CPUMillicores: 20, MemoryBytes: 36 * mi}, vpn.Size(5))
	assert.Equal(t, resourcesModel.Amount{CPUMillicores: 50, MemoryBytes: 64 * mi}, vpn.Size(1000), "the maximum caps it")
	assert.Equal(t, vpn.Size(0), vpn.Size(-3), "no negative users")

	// A zero maximum is no maximum; an unreported sizing adds nothing.
	open := GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 1}, PerUnit: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 1}}
	assert.Equal(t, int64(101), open.Size(100).CPUMillicores)
	assert.False(t, GroupPodSizing{}.Reported())
	assert.Equal(t, resourcesModel.Amount{}, GroupPodSizing{}.Size(10))
}

func TestSizesForUsesUsersForTheVPNAndInternetLabsForTheGateway(t *testing.T) {
	l := LimitsFeature{
		VPN:     GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 10}, PerUnit: resourcesModel.Amount{CPUMillicores: 1}},
		Gateway: GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 20}, PerUnit: resourcesModel.Amount{CPUMillicores: 5}},
	}
	sizes := l.SizesFor(GroupPlan{MaxUsers: 8, InternetLabs: 2})
	assert.Equal(t, int64(18), sizes.VPN.CPUMillicores)
	assert.Equal(t, int64(30), sizes.Gateway.CPUMillicores)
	assert.Equal(t, int64(48), sizes.Total().CPUMillicores)
}

func TestFitsChecksTheDeviceMaximaAndTheLabSize(t *testing.T) {
	l := LimitsFeature{DeviceMaxCPUMillicores: 500, DeviceMaxMemoryBytes: 2 * gi, LabMaxDevices: 32}
	assert.Nil(t, l.Fits(PlacementNeed{Device: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 * gi}, LabDevices: 32}))
	v := l.Fits(PlacementNeed{Device: resourcesModel.Amount{CPUMillicores: 600, MemoryBytes: gi}})
	assert.Equal(t, &FitViolation{Resource: FitCPU, Requested: 600, Max: 500}, v)
	v = l.Fits(PlacementNeed{Device: resourcesModel.Amount{CPUMillicores: 100, MemoryBytes: 3 * gi}})
	assert.Equal(t, &FitViolation{Resource: FitMemory, Requested: 3 * gi, Max: 2 * gi}, v)
	v = l.Fits(PlacementNeed{LabDevices: 33})
	assert.Equal(t, &FitViolation{Resource: FitDevices, Requested: 33, Max: 32}, v)
	// 0 is no limit.
	assert.Nil(t, LimitsFeature{}.Fits(PlacementNeed{Device: resourcesModel.Amount{CPUMillicores: 9999}, LabDevices: 64}))
}

func TestUnmetRequirementsAreTheFrameAndThirtyTwoDevices(t *testing.T) {
	frame := resourcesModel.Amount{CPUMillicores: 250, MemoryBytes: gi}
	assert.Empty(t, LimitsFeature{DeviceMaxCPUMillicores: 250, DeviceMaxMemoryBytes: gi, LabMaxDevices: 32}.UnmetRequirements(frame), "exactly the frame is enough")
	assert.Empty(t, LimitsFeature{}.UnmetRequirements(frame), "no limit meets them")
	unmet := LimitsFeature{DeviceMaxCPUMillicores: 100, DeviceMaxMemoryBytes: 512 * mi, LabMaxDevices: 16}.UnmetRequirements(frame)
	assert.Equal(t, []FitViolation{
		{Resource: FitDeviceCPU, Requested: 250, Max: 100},
		{Resource: FitDeviceMemory, Requested: gi, Max: 512 * mi},
		{Resource: FitDevices, Requested: 32, Max: 16},
	}, unmet)
	assert.Len(t, LimitsFeature{LabMaxDevices: 31}.UnmetRequirements(frame), 1, "fewer than 32 devices per lab")
}

func TestPlacementContextCarriesTheNeedAndTheSizes(t *testing.T) {
	ctx := context.Background()
	assert.False(t, PlacementNeedFrom(ctx).Known())
	need := PlacementNeed{LabDevices: 3, Plan: GroupPlan{MaxUsers: 5}}
	assert.Equal(t, need, PlacementNeedFrom(WithPlacementNeed(ctx, need)))
	_, ok := GroupSizesFrom(ctx)
	assert.False(t, ok)
	sizes := GroupSizes{VPN: resourcesModel.Amount{CPUMillicores: 7}}
	got, ok := GroupSizesFrom(WithGroupSizes(ctx, sizes))
	assert.True(t, ok)
	assert.Equal(t, sizes, got)
}
