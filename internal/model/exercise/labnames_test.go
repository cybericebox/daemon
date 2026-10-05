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
	id := uuid.Must(uuid.NewV7())
	taken := "sw-" + strings.ReplaceAll(id.String(), "-", "")
	names := exerciseModel.LabDeviceNames([]exerciseModel.Device{
		{ID: uuid.Must(uuid.NewV7()), Name: taken, Type: exerciseModel.DeviceTypeContainer},
		{ID: id, Name: "sw", Type: exerciseModel.DeviceTypeHub},
	})
	if names[1] == taken || len(names[1]) != len(taken) || !strings.HasSuffix(names[1], "0") {
		t.Fatalf("names = %v", names)
	}
}
