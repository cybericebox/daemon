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
	assert.Equal(t, Amount{16, 64 * mi}, p.Block)
	assert.Equal(t, []Amount{{16, 64 * mi}, {32, 128 * mi}, {125, 512 * mi}, {250, gi}, {500, 2 * gi}, {1000, 4 * gi}}, amounts(p))
	assert.Equal(t, Amount{16, 64 * mi}, p.Default())
	assert.Equal(t, Amount{250, gi}, p.Frame)
	assert.Equal(t, Amount{1000, 4 * gi}, p.Ceiling)
	var counts []int
	for _, preset := range p.Presets {
		counts = append(counts, preset.Blocks)
	}
	assert.Equal(t, []int{1, 2, 8, 16, 32, 64}, counts)
}

func TestParsePolicyRefusesDisagreeingSettings(t *testing.T) {
	const presets = "micro=1,small=2,medium=8,large=16,xlarge=32,huge=64"
	for name, args := range map[string]struct {
		block, presets, def string
		frame, ceiling      int
	}{
		"count not a multiple of the smaller": {"15625u/64Mi", "a=1,b=2,c=5", "a", 2, 5},
		"default not a preset":                {"15625u/64Mi", presets, "tiny", 16, 64},
		"frame not a preset":                  {"15625u/64Mi", presets, "micro", 12, 64},
		"ceiling not a preset":                {"15625u/64Mi", presets, "micro", 16, 48},
		"ceiling below the frame":             {"15625u/64Mi", presets, "micro", 32, 16},
		"preset above the ceiling":            {"15625u/64Mi", presets, "micro", 16, 32},
		"duplicate id":                        {"15625u/64Mi", "a=1,a=2", "a", 2, 2},
		"duplicate count":                     {"15625u/64Mi", "a=1,b=1", "a", 1, 1},
		"bad count":                           {"15625u/64Mi", "a=lots", "a", 1, 1},
		"zero count":                          {"15625u/64Mi", "a=0", "a", 1, 1},
		"bad block":                           {"lots/64Mi", presets, "micro", 16, 64},
		"block without memory":                {"16m", presets, "micro", 16, 64},
		"no presets":                          {"15625u/64Mi", "", "micro", 16, 64},
	} {
		_, err := ParsePolicy(args.block, args.presets, args.def, args.frame, args.ceiling)
		assert.Error(t, err, name)
	}
}

func TestBlocksRoundUpOverBothResources(t *testing.T) {
	p := DefaultPolicy()
	assert.Equal(t, 0, p.BlocksOf(Amount{}))
	assert.Equal(t, 1, p.BlocksOf(Amount{1, 1}))
	assert.Equal(t, 1, p.BlocksOf(Amount{16, 64 * mi}))
	assert.Equal(t, 2, p.BlocksOf(Amount{17, 64 * mi}), "cpu over one block")
	for _, preset := range p.Presets {
		assert.Equal(t, preset.Blocks, p.BlocksOf(preset.Amount), preset.ID)
	}
	assert.Equal(t, 3, p.BlocksOf(Amount{16, 129 * mi}), "memory over two blocks")
	assert.Equal(t, Amount{47, 192 * mi}, p.RoundUp(Amount{40, 100 * mi}))
	assert.Equal(t, Amount{}, p.RoundUp(Amount{}))
}

func TestLargestFitsTheAgentMaximum(t *testing.T) {
	p := DefaultPolicy()
	got, ok := p.Largest(Amount{3000, 3 * gi})
	require.True(t, ok)
	assert.Equal(t, "xlarge", got.ID, "3Gi holds 2Gi but not 4Gi")
	got, _ = p.Largest(Amount{4000, 16 * gi})
	assert.Equal(t, "huge", got.ID)
	got, _ = p.Largest(Amount{100, 100 * mi})
	assert.Equal(t, "micro", got.ID, "limited by memory")
	_, ok = p.Largest(Amount{10, 32 * mi})
	assert.False(t, ok)
}

func TestResolve(t *testing.T) {
	p := DefaultPolicy()
	micro := Amount{16, 64 * mi}
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
	assert.Equal(t, "16m", none.CPULimit)
	assert.Equal(t, "64Mi", none.MemoryLimit)
}

func TestTotalsCountOnlyContainers(t *testing.T) {
	p := DefaultPolicy()
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		container("a", ""),
		container("b", "small"),
		{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}}
	assert.Equal(t, Totals{Devices: 2, Blocks: 3, Amount: Amount{48, 192 * mi}}, p.Total(topo))
}

func TestVariantRangeAndOutside(t *testing.T) {
	p := DefaultPolicy()
	variants := []exerciseModel.Variant{
		{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{container("a", "")}}},
		{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{container("heavy", "xlarge"), container("huge", "huge")}}},
	}
	r := p.VariantRange(variants)
	assert.Equal(t, Totals{Devices: 1, Blocks: 1, Amount: Amount{16, 64 * mi}}, r.Min)
	assert.Equal(t, Totals{Devices: 2, Blocks: 96, Amount: Amount{1500, 6 * gi}}, r.Max)

	out := p.OutsideFrame(variants)
	require.Len(t, out, 2)
	assert.Equal(t, "heavy", out[0].Name)
	assert.Equal(t, 32, out[0].Blocks)
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
	newElevation := func() Elevation {
		return Elevation{Status: ElevationPending, Requested: []Approval{{DeviceID: id, Name: "db", Amount: p.Amount(64)}}}
	}
	for name, tc := range map[string]struct {
		values []Approval
		ok     bool
	}{
		"nil approves what was requested":     {nil, true},
		"the requested block":                 {[]Approval{{DeviceID: id, Amount: p.Amount(64)}}, true},
		"a smaller offered block":             {[]Approval{{DeviceID: id, Amount: p.Amount(32)}}, true},
		"a block the platform does not offer": {[]Approval{{DeviceID: id, Amount: p.Amount(24)}}, false},
		"not a whole block":                   {[]Approval{{DeviceID: id, Amount: Amount{400, 2 * gi}}}, false},
		"a device that was not requested":     {[]Approval{{DeviceID: uuid.Must(uuid.NewV7()), Amount: p.Amount(32)}}, false},
		"the same device twice":               {[]Approval{{DeviceID: id, Amount: p.Amount(32)}, {DeviceID: id, Amount: p.Amount(16)}}, false},
	} {
		e := newElevation()
		err := e.Approve(tc.values, p, uuid.Must(uuid.NewV7()), "", now)
		if tc.ok {
			assert.NoError(t, err, name)
		} else {
			assert.Error(t, err, name)
		}
	}
	asked := Elevation{Status: ElevationPending, Requested: []Approval{{DeviceID: id, Amount: p.Amount(32)}}}
	assert.Error(t, asked.Approve([]Approval{{DeviceID: id, Amount: p.Amount(64)}}, p, uuid.Nil, "", now), "never larger than asked")
}
