package exerciseModel_test

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func deviceWithEnv(device string, envNames ...string) exerciseModel.Device {
	d := exerciseModel.Device{Name: device, Type: exerciseModel.DeviceTypeNode}
	for _, name := range envNames {
		d.Bootstrap.Env = append(d.Bootstrap.Env, exerciseModel.EnvVar{Name: name, Value: "v", Secret: true})
	}
	return d
}

func variantOf(devices ...exerciseModel.Device) exerciseModel.Variant {
	return exerciseModel.Variant{Topology: exerciseModel.Topology{Devices: devices}}
}

// A device or variable name with ':' would run into its neighbour in the context of a secret variable; a draft that has one
// is refused, with the position and the field so the editor can mark it. Stored data is not touched.
func TestValidateDeviceNames_RefusesTheSeparatorInANodeOrEnvName(t *testing.T) {
	err := exerciseModel.ValidateDeviceNames([]exerciseModel.Variant{variantOf(deviceWithEnv("web", "A")), variantOf(deviceWithEnv("a:b", "C"))})
	require.ErrorIs(t, err, exerciseModel.ErrNameHasSeparator.Err())
	assert.Equal(t, map[string]any{"variant": 2, "device": "a:b", "field": "device"}, publicContext(t, err))

	err = exerciseModel.ValidateDeviceNames([]exerciseModel.Variant{variantOf(deviceWithEnv("db", "OK", "B:C"))})
	require.ErrorIs(t, err, exerciseModel.ErrNameHasSeparator.Err())
	assert.Equal(t, map[string]any{"variant": 1, "device": "db", "field": "env"}, publicContext(t, err))
}

func TestValidateDeviceNames_AllowsNamesWithoutTheSeparatorAndDisplayNamesOfSwitches(t *testing.T) {
	hub := exerciseModel.Device{Name: "lab: main hub", Type: exerciseModel.DeviceTypeHub} // a display name, holds no variables
	require.NoError(t, exerciseModel.ValidateDeviceNames([]exerciseModel.Variant{variantOf(deviceWithEnv("web", "DB_PASS", "TOKEN"), hub)}))
}

// The error has a code and a text in both languages of the catalog.
func TestNameHasSeparatorIsInTheCatalogInBothLanguages(t *testing.T) {
	code := exerciseModel.ErrNameHasSeparator.Err().StatusCode().FullCode()
	for _, file := range []string{"errors.en.json", "errors.uk.json"} {
		raw, err := os.ReadFile("../../../error-catalog/" + file)
		require.NoError(t, err)
		var catalog map[string]string
		require.NoError(t, json.Unmarshal(raw, &catalog))
		assert.NotEmpty(t, catalog[strconv.Itoa(code)], "%s has no text for %d", file, code)
	}
}
