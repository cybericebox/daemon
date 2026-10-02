package resourcesModel

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

const mi, gi = 1 << 20, 1 << 30

func container(name string, mutate func(*exerciseModel.Device)) exerciseModel.Device {
	d := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeContainer}
	if mutate != nil {
		mutate(&d)
	}
	return d
}

func TestDefaultPolicyIsValid(t *testing.T) {
	p := DefaultPolicy()
	require.NoError(t, p.Validate())
	assert.Equal(t, Amount{25, 64 * mi}, p.Default())
	assert.Equal(t, Amount{250, gi}, p.Frame)
	assert.Equal(t, Amount{1000, 4 * gi}, p.Ceiling)
}

func TestParsePolicyMatchesDefaults(t *testing.T) {
	p, err := ParsePolicy("micro=25m/64Mi,small=50m/128Mi,medium=125m/512Mi,large=250m/1Gi", "micro", "250m/1Gi", "1/4Gi")
	require.NoError(t, err)
	assert.Equal(t, DefaultPolicy(), p)
}

func TestParsePolicyRefusesDisagreeingSettings(t *testing.T) {
	for name, args := range map[string][4]string{
		"preset above the frame":    {"big=500m/1Gi", "big", "250m/1Gi", "1/4Gi"},
		"default not a preset":      {"micro=25m/64Mi", "tiny", "250m/1Gi", "1/4Gi"},
		"frame above the ceiling":   {"micro=25m/64Mi", "micro", "2/1Gi", "1/4Gi"},
		"duplicate preset":          {"a=25m/64Mi,a=50m/64Mi", "a", "250m/1Gi", "1/4Gi"},
		"bad quantity":              {"micro=lots/64Mi", "micro", "250m/1Gi", "1/4Gi"},
		"missing memory":            {"micro=25m", "micro", "250m/1Gi", "1/4Gi"},
		"no presets":                {"", "micro", "250m/1Gi", "1/4Gi"},
		"zero cpu in the frame":     {"micro=25m/64Mi", "micro", "0/1Gi", "1/4Gi"},
		"ceiling memory not parsed": {"micro=25m/64Mi", "micro", "250m/1Gi", "1/x"},
	} {
		_, err := ParsePolicy(args[0], args[1], args[2], args[3])
		assert.Error(t, err, name)
	}
}

func TestResolve(t *testing.T) {
	p := DefaultPolicy()
	cases := map[string]struct {
		mutate func(*exerciseModel.Device)
		want   Amount
	}{
		"nothing is the default preset": {nil, Amount{25, 64 * mi}},
		"preset":                        {func(d *exerciseModel.Device) { d.ResourcePreset = "medium" }, Amount{125, 512 * mi}},
		"unknown preset is the default": {func(d *exerciseModel.Device) { d.ResourcePreset = "gigantic" }, Amount{25, 64 * mi}},
		"preset wins over custom": {func(d *exerciseModel.Device) {
			d.ResourcePreset = "small"
			d.Resources = &exerciseModel.DeviceResources{CPULimit: "900m", MemoryLimit: "3Gi"}
		}, Amount{50, 128 * mi}},
		"custom limits": {func(d *exerciseModel.Device) {
			d.Resources = &exerciseModel.DeviceResources{CPULimit: "100m", MemoryLimit: "200Mi"}
		}, Amount{100, 200 * mi}},
		"custom request only counts": {func(d *exerciseModel.Device) {
			d.Resources = &exerciseModel.DeviceResources{CPURequest: "1", MemoryRequest: "2Gi"}
		}, Amount{1000, 2 * gi}},
		"custom cpu only keeps the default memory": {func(d *exerciseModel.Device) {
			d.Resources = &exerciseModel.DeviceResources{CPULimit: "75m"}
		}, Amount{75, 64 * mi}},
	}
	for name, tc := range cases {
		assert.Equal(t, tc.want, p.Resolve(container("d", tc.mutate)), name)
	}
}

func TestExplicitSetsRequestsEqualLimits(t *testing.T) {
	r := DefaultPolicy().Explicit(container("d", func(d *exerciseModel.Device) { d.ResourcePreset = "large" }))
	assert.Equal(t, "250m", r.CPURequest)
	assert.Equal(t, "250m", r.CPULimit)
	assert.Equal(t, "1Gi", r.MemoryRequest)
	assert.Equal(t, "1Gi", r.MemoryLimit)
	none := DefaultPolicy().Explicit(container("d", nil))
	assert.Equal(t, "25m", none.CPULimit)
	assert.Equal(t, "64Mi", none.MemoryLimit)
}

func TestTotalsCountOnlyContainers(t *testing.T) {
	p := DefaultPolicy()
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{
		container("a", nil),
		container("b", func(d *exerciseModel.Device) { d.ResourcePreset = "small" }),
		{ID: uuid.Must(uuid.NewV7()), Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}}
	assert.Equal(t, Totals{Devices: 2, Amount: Amount{75, 192 * mi}}, p.Total(topo))
}

func TestVariantRangeAndOutside(t *testing.T) {
	p := DefaultPolicy()
	heavy := container("heavy", func(d *exerciseModel.Device) {
		d.Resources = &exerciseModel.DeviceResources{CPULimit: "500m", MemoryLimit: "2Gi"}
	})
	huge := container("huge", func(d *exerciseModel.Device) {
		d.Resources = &exerciseModel.DeviceResources{CPULimit: "2", MemoryLimit: "1Gi"}
	})
	variants := []exerciseModel.Variant{
		{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{container("a", nil)}}},
		{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{heavy, huge}}},
	}
	r := p.VariantRange(variants)
	assert.Equal(t, Totals{Devices: 1, Amount: Amount{25, 64 * mi}}, r.Min)
	assert.Equal(t, Totals{Devices: 2, Amount: Amount{2500, 3 * gi}}, r.Max)

	out := p.OutsideFrame(variants)
	require.Len(t, out, 2)
	assert.Equal(t, "heavy", out[0].Name)
	assert.False(t, out[0].AboveCeiling)
	assert.Equal(t, "huge", out[1].Name)
	assert.True(t, out[1].AboveCeiling)
}

func TestCoveredNeedsEveryValueAtOrBelowTheApproval(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	approved := []Approval{{DeviceID: id, Amount: Amount{500, 2 * gi}}}
	assert.True(t, Covered(Outside{DeviceID: id, Amount: Amount{500, 2 * gi}}, approved))
	assert.True(t, Covered(Outside{DeviceID: id, Amount: Amount{300, gi}}, approved), "lowering keeps the approval")
	assert.False(t, Covered(Outside{DeviceID: id, Amount: Amount{600, 2 * gi}}, approved), "raising cpu needs a new approval")
	assert.False(t, Covered(Outside{DeviceID: id, Amount: Amount{500, 3 * gi}}, approved), "raising memory needs a new approval")
	assert.False(t, Covered(Outside{DeviceID: uuid.Must(uuid.NewV7()), Amount: Amount{1, 1}}, approved), "another device")
}
