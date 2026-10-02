package resourcesModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

const mi, gi = 1 << 20, 1 << 30

func container(name string, preset string) exerciseModel.Device {
	return exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeContainer, ResourcePreset: preset}
}

func amounts(p Policy) []Amount {
	var out []Amount
	for _, preset := range p.Presets {
		out = append(out, preset.Amount)
	}
	return out
}

func TestDefaultPolicyIsValid(t *testing.T) {
	p := DefaultPolicy()
	require.NoError(t, p.Validate())
	assert.Equal(t, Amount{7, 32 * mi}, p.Block)
	assert.Equal(t, Amount{15, 64 * mi}, p.Default())
	assert.Equal(t, Amount{250, gi}, p.Frame)
	assert.Equal(t, Amount{1000, 4 * gi}, p.Ceiling)
	assert.Equal(t, []Amount{{7, 32 * mi}, {15, 64 * mi}, {31, 128 * mi}, {62, 256 * mi}, {125, 512 * mi}, {250, gi}, {500, 2 * gi}, {1000, 4 * gi}}, amounts(p))
	var counts []int
	for _, preset := range p.Presets {
		counts = append(counts, preset.Blocks)
	}
	assert.Equal(t, []int{1, 2, 4, 8, 16, 32, 64, 128}, counts)
	assert.Equal(t, 32, p.FrameBlocks)
	assert.Equal(t, 128, p.CeilingBlocks)
}

func TestParsePolicyRefusesDisagreeingSettings(t *testing.T) {
	const presets = "nano=32Mi,micro=64Mi,small=128Mi,large=1Gi,max=4Gi"
	for name, args := range map[string]struct{ presets, def, frame, ceiling string }{
		"size not a multiple of the smaller": {"a=32Mi,b=64Mi,c=96Mi", "a", "b", "c"},
		"default not a preset":               {presets, "tiny", "large", "max"},
		"frame not a preset":                 {presets, "micro", "huge", "max"},
		"ceiling not a preset":               {presets, "micro", "large", "huge"},
		"ceiling below the frame":            {presets, "micro", "max", "large"},
		"preset above the ceiling":           {presets, "micro", "small", "large"},
		"duplicate id":                       {"a=32Mi,a=64Mi", "a", "a", "a"},
		"duplicate memory":                   {"a=32Mi,b=32Mi", "a", "a", "a"},
		"bad memory":                         {"a=lots", "a", "a", "a"},
		"zero memory":                        {"a=0", "a", "a", "a"},
		"no presets":                         {"", "micro", "large", "max"},
	} {
		_, err := ParsePolicy(args.presets, args.def, args.frame, args.ceiling)
		assert.Error(t, err, name)
	}
}

func TestCPUFollowsMemoryRoundedDown(t *testing.T) {
	assert.Equal(t, int64(7), cpuFor(32*mi), "7.8 rounds down")
	assert.Equal(t, int64(15), cpuFor(64*mi))
	assert.Equal(t, int64(31), cpuFor(128*mi))
	assert.Equal(t, int64(62), cpuFor(256*mi))
	assert.Equal(t, int64(1000), cpuFor(4*gi))
}

func TestBlocksRoundUpToThePresetThatHoldsBoth(t *testing.T) {
	p := DefaultPolicy()
	assert.Equal(t, 0, p.BlocksOf(Amount{}))
	assert.Equal(t, 1, p.BlocksOf(Amount{1, 1}))
	for _, preset := range p.Presets {
		assert.Equal(t, preset.Blocks, p.BlocksOf(preset.Amount), preset.ID)
		assert.Equal(t, preset.Amount, p.RoundUp(preset.Amount), preset.ID)
	}
	assert.Equal(t, Amount{15, 64 * mi}, p.RoundUp(Amount{10, 40 * mi}), "memory sets the size")
	assert.Equal(t, Amount{31, 128 * mi}, p.RoundUp(Amount{16, 40 * mi}), "cpu above 15m takes the next size up")
	assert.Equal(t, Amount{}, p.RoundUp(Amount{}))
	assert.Equal(t, Amount{2000, 8 * gi}, p.RoundUp(Amount{2000, 8 * gi}), "above the largest preset is left as it is")
}

func TestLargestFitsTheAgentMaximum(t *testing.T) {
	p := DefaultPolicy()
	got, ok := p.Largest(Amount{3000, 3 * gi})
	require.True(t, ok)
	assert.Equal(t, "xlarge", got.ID, "3Gi holds 2Gi but not 4Gi")
	got, _ = p.Largest(Amount{4000, 16 * gi})
	assert.Equal(t, "max", got.ID)
	got, _ = p.Largest(Amount{100, 100 * mi})
	assert.Equal(t, "micro", got.ID, "limited by memory")
	_, ok = p.Largest(Amount{5, 16 * mi})
	assert.False(t, ok)
}

func TestResolve(t *testing.T) {
	p := DefaultPolicy()
	micro := Amount{15, 64 * mi}
	assert.Equal(t, micro, p.Resolve(container("d", "")), "nothing is the default preset")
	assert.Equal(t, Amount{125, 512 * mi}, p.Resolve(container("d", "medium")))
	assert.Equal(t, micro, p.Resolve(container("d", "gigantic")), "unknown preset is the default")
	custom := container("d", "")
	custom.Resources = &exerciseModel.DeviceResources{CPULimit: "900m", MemoryLimit: "3Gi"}
	assert.Equal(t, micro, p.Resolve(custom), "custom values are ignored")
	assert.True(t, p.InvalidPreset(container("d", "gigantic")))
	assert.False(t, p.InvalidPreset(container("d", "")))
}

func TestExplicitSetsRequestsEqualLimits(t *testing.T) {
	r := DefaultPolicy().Explicit(container("d", "large"))
	assert.Equal(t, "250m", r.CPURequest)
	assert.Equal(t, "250m", r.CPULimit)
	assert.Equal(t, "1Gi", r.MemoryRequest)
	assert.Equal(t, "1Gi", r.MemoryLimit)
	none := DefaultPolicy().Explicit(container("d", ""))
	assert.Equal(t, "15m", none.CPULimit)
	assert.Equal(t, "64Mi", none.MemoryLimit)
}

func TestTotalsCountOnlyContainers(t *testing.T) {
	p := DefaultPolicy()
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		container("a", ""),
		container("b", "small"),
		{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}}
	assert.Equal(t, Totals{Devices: 2, Blocks: 6, Amount: Amount{46, 192 * mi}}, p.Total(topo))
}

func TestVariantRangeAndOutside(t *testing.T) {
	p := DefaultPolicy()
	variants := []exerciseModel.Variant{
		{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{container("a", "")}}},
		{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{container("heavy", "xlarge"), container("max", "max")}}},
	}
	r := p.VariantRange(variants)
	assert.Equal(t, Totals{Devices: 1, Blocks: 2, Amount: Amount{15, 64 * mi}}, r.Min)
	assert.Equal(t, Totals{Devices: 2, Blocks: 192, Amount: Amount{1500, 6 * gi}}, r.Max)

	out := p.OutsideFrame(variants)
	require.Len(t, out, 2)
	assert.Equal(t, "heavy", out[0].Name)
	assert.Equal(t, 64, out[0].Blocks)
	assert.False(t, out[0].AboveCeiling)
	assert.False(t, out[1].AboveCeiling, "the ceiling block itself is allowed")
}

func TestCoveredNeedsEveryValueAtOrBelowTheApproval(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	approved := []Approval{{DeviceID: id, Amount: Amount{512, 2 * gi}}}
	assert.True(t, Covered(Outside{DeviceID: id, Amount: Amount{512, 2 * gi}}, approved))
	assert.True(t, Covered(Outside{DeviceID: id, Amount: Amount{256, gi}}, approved), "lowering keeps the approval")
	assert.False(t, Covered(Outside{DeviceID: id, Amount: Amount{1024, 4 * gi}}, approved), "a larger block needs a new approval")
	assert.False(t, Covered(Outside{DeviceID: uuid.Must(uuid.NewV7()), Amount: Amount{1, 1}}, approved), "another device")
}

func TestApproveTakesARequestedOrSmallerOfferedBlock(t *testing.T) {
	p := DefaultPolicy()
	id := uuid.Must(uuid.NewV7())
	now := time.Now()
	max, xlarge, large := p.Amount(128), p.Amount(64), p.Amount(32)
	newElevation := func(asked Amount) Elevation {
		return Elevation{Status: ElevationPending, Requested: []Approval{{DeviceID: id, Name: "db", Amount: asked}}}
	}
	for name, tc := range map[string]struct {
		asked  Amount
		values []Approval
		ok     bool
	}{
		"nil approves what was requested":     {max, nil, true},
		"the requested block":                 {max, []Approval{{DeviceID: id, Amount: max}}, true},
		"a smaller offered block":             {max, []Approval{{DeviceID: id, Amount: xlarge}}, true},
		"a block the platform does not offer": {max, []Approval{{DeviceID: id, Amount: p.Amount(96)}}, false},
		"not a preset size":                   {max, []Approval{{DeviceID: id, Amount: Amount{400, 2 * gi}}}, false},
		"a device that was not requested":     {max, []Approval{{DeviceID: uuid.Must(uuid.NewV7()), Amount: xlarge}}, false},
		"the same device twice":               {max, []Approval{{DeviceID: id, Amount: xlarge}, {DeviceID: id, Amount: large}}, false},
		"larger than asked":                   {xlarge, []Approval{{DeviceID: id, Amount: max}}, false},
	} {
		e := newElevation(tc.asked)
		err := e.Approve(tc.values, p, uuid.Must(uuid.NewV7()), "", now)
		if tc.ok {
			assert.NoError(t, err, name)
		} else {
			assert.Error(t, err, name)
		}
	}
}
