package exerciseModel_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
)

func TestDevice_FormerContainerLoadsAsNodeWithContainerProfile(t *testing.T) {
	var device exerciseModel.Device
	require.NoError(t, json.Unmarshal([]byte(`{"name":"web","type":"container","image":"nginx:1"}`), &device))
	assert.Equal(t, exerciseModel.DeviceTypeNode, device.Type)
	assert.Equal(t, exerciseModel.NodeProfileContainer, device.Profile)

	require.NoError(t, json.Unmarshal([]byte(`{"name":"vm1","type":"node","profile":"microvm"}`), &device))
	assert.Equal(t, exerciseModel.NodeProfileMicroVM, device.Profile)

	require.NoError(t, json.Unmarshal([]byte(`{"name":"sw","type":"unmanaged-switch"}`), &device))
	assert.Equal(t, exerciseModel.DeviceTypeUnmanagedSwitch, device.Type)
	assert.Empty(t, device.Profile)
}

func TestDevice_EffectiveProfile(t *testing.T) {
	assert.Equal(t, exerciseModel.NodeProfileContainer, exerciseModel.Device{Type: exerciseModel.DeviceTypeContainer}.EffectiveProfile())
	assert.Equal(t, exerciseModel.NodeProfileContainer, exerciseModel.Device{Type: exerciseModel.DeviceTypeNode}.EffectiveProfile())
	assert.Equal(t, exerciseModel.NodeProfileVM, exerciseModel.Device{Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileVM}.EffectiveProfile())
	assert.Empty(t, exerciseModel.Device{Type: exerciseModel.DeviceTypeHub}.EffectiveProfile())
}

func TestValidateStructure_NodeProfile(t *testing.T) {
	for name, tc := range map[string]struct {
		device exerciseModel.Device
		valid  bool
	}{
		"node, no profile":      {exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode}, true},
		"node, microvm":         {exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileMicroVM}, true},
		"node, vm is in model":  {exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileVM}, true},
		"node, unknown profile": {exerciseModel.Device{Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: "wasm"}, false},
		"switch with a profile": {exerciseModel.Device{Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch, Profile: exerciseModel.NodeProfileContainer}, false},
	} {
		t.Run(name, func(t *testing.T) {
			variant := validVariant()
			variant.Topology.Devices = []exerciseModel.Device{tc.device}
			err := (&exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}).ValidateStructure()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.True(t, errors.Is(err, exerciseModel.ErrNodeProfileInvalid.Err()), "got %v", err)
			}
		})
	}
}

func TestSupportedProfiles_DefaultIsTheContainerOnly(t *testing.T) {
	assert.Equal(t, []exerciseModel.NodeProfile{exerciseModel.NodeProfileContainer}, exerciseModel.DefaultSupportedProfiles().List())
}

func TestSupportedProfiles_WithValues(t *testing.T) {
	got, err := exerciseModel.SupportedProfiles{}.WithValues(platformSettingsModel.Values{"Container": 1, "MicroVM": 1, "VM": 0})
	require.NoError(t, err)
	assert.Equal(t, []exerciseModel.NodeProfile{exerciseModel.NodeProfileContainer, exerciseModel.NodeProfileMicroVM}, got.List())
	assert.Equal(t, platformSettingsModel.Values{"Container": 1, "MicroVM": 1, "VM": 0}, got.Values())

	_, err = exerciseModel.SupportedProfiles{}.WithValues(platformSettingsModel.Values{"Container": 0, "MicroVM": 0, "VM": 0})
	require.ErrorIs(t, err, platformSettingsModel.ErrNodeProfilesNoneSupported.Err())
	_, err = exerciseModel.SupportedProfiles{}.WithValues(platformSettingsModel.Values{"Container": 2, "MicroVM": 0, "VM": 0})
	require.ErrorIs(t, err, platformSettingsModel.ErrSettingValueOutOfRange.Err())
	_, err = exerciseModel.SupportedProfiles{}.WithValues(platformSettingsModel.Values{"Container": 1})
	require.ErrorIs(t, err, platformSettingsModel.ErrSettingValueMissing.Err())
}

func TestSupportedProfiles_UnsupportedProfileNamesTheNode(t *testing.T) {
	node := func(name string, profile exerciseModel.NodeProfile) exerciseModel.Device {
		return exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeNode, Profile: profile}
	}
	variants := []exerciseModel.Variant{
		{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{node("web", ""), node("db", exerciseModel.NodeProfileContainer)}}},
		{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{node("web", ""), node("kernel", exerciseModel.NodeProfileMicroVM)}}},
	}
	err := exerciseModel.DefaultSupportedProfiles().UnsupportedProfile(variants)
	require.ErrorIs(t, err, exerciseModel.ErrNodeProfileNotSupported.Err())
	assert.Equal(t, map[string]any{"variant": 2, "device": "kernel", "profile": "microvm"}, publicContext(t, err))

	ticked := exerciseModel.SupportedProfiles{Container: true, MicroVM: true}
	require.NoError(t, ticked.UnsupportedProfile(variants))
	assert.Equal(t, []exerciseModel.NodeProfile{exerciseModel.NodeProfileContainer, exerciseModel.NodeProfileMicroVM}, exerciseModel.NodeProfilesUsed(variants))
}
