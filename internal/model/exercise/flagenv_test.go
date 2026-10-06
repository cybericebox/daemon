package exerciseModel_test

import (
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestWithFlagEnv_AddsTheFlagWithoutMutatingTheSource(t *testing.T) {
	deviceID := uuid.Must(uuid.NewV7())
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{
		{ID: deviceID, Name: "web", EnvVars: []exerciseModel.EnvVar{{Name: "MODE", Value: "prod"}}},
		{ID: uuid.Must(uuid.NewV7()), Name: "db"},
	}}
	got, err := topology.WithFlagEnv([]exerciseModel.FlagLink{{DeviceID: deviceID, Var: "FLAG", Flag: "ICE{a}"}, {DeviceID: deviceID, Var: "SKIPPED"}})
	if err != nil {
		t.Fatal(err)
	}
	env := got.Devices[0].EnvVars
	if len(env) != 2 || env[0].Name != "MODE" || env[1].Name != "FLAG" || env[1].Value != "ICE{a}" {
		t.Fatalf("env = %+v", env)
	}
	if len(topology.Devices[0].EnvVars) != 1 {
		t.Fatalf("source topology mutated: %+v", topology.Devices[0].EnvVars)
	}
}

func TestWithFlagEnv_RejectsAnAuthorVarOfTheSameName(t *testing.T) {
	deviceID := uuid.Must(uuid.NewV7())
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: deviceID, Name: "web", EnvVars: []exerciseModel.EnvVar{{Name: "FLAG", Value: "x"}}}}}
	_, err := topology.WithFlagEnv([]exerciseModel.FlagLink{{DeviceID: deviceID, Var: "FLAG", Flag: "ICE{a}"}})
	if !errors.Is(err, exerciseModel.ErrFlagEnvironmentConflict.Err()) {
		t.Fatalf("err = %v", err)
	}
}

func TestVariantFlagLinks_OnlyLinkedTasksWithAVar(t *testing.T) {
	deviceID, taskID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variant := exerciseModel.Variant{Tasks: []exerciseModel.Task{
		{ID: taskID, Name: "one", LinkedDeviceID: uuid.NullUUID{UUID: deviceID, Valid: true}, DeviceFlagVar: "FLAG", Flag: []string{"ICE{a}"}},
		{ID: uuid.Must(uuid.NewV7()), Name: "no device"},
	}}
	links := variant.FlagLinks()
	if len(links) != 1 || links[0].TaskID != taskID || links[0].DeviceID != deviceID || links[0].Var != "FLAG" || links[0].Name != "one" {
		t.Fatalf("links = %+v", links)
	}
}
