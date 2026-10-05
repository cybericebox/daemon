package exerciseModel_test

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestLabDeviceNamesKeepContainersAndHashSwitches(t *testing.T) {
	sw := uuid.Must(uuid.NewV7())
	devices := []exerciseModel.Device{
		{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer},
		{ID: sw, Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch},
	}
	names := exerciseModel.LabDeviceNames(devices)
	if names[0] != "web" || names[1] != "sw-"+strings.ReplaceAll(sw.String(), "-", "") || len(names[1]) != 35 {
		t.Fatalf("names = %v", names)
	}
}

func TestLabDeviceNamesAvoidsTakenNames(t *testing.T) {
	// A fixed id that does not end in "0": a random one ended in "0" one time in 16, so the
	// first free suffix was "1" and the assertion below failed.
	id := uuid.Must(uuid.FromString("01a10b00-0000-7000-8000-000000000001"))
	taken := "sw-" + strings.ReplaceAll(id.String(), "-", "")
	names := exerciseModel.LabDeviceNames([]exerciseModel.Device{
		{ID: uuid.Must(uuid.NewV7()), Name: taken, Type: exerciseModel.DeviceTypeContainer},
		{ID: id, Name: "sw", Type: exerciseModel.DeviceTypeHub},
	})
	if names[1] == taken || len(names[1]) != len(taken) || !strings.HasSuffix(names[1], "0") {
		t.Fatalf("names = %v", names)
	}
}
