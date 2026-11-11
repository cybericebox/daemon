package labagent

import (
	"testing"

	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The bootstrap phase and reason of a device reach the lab status that staff and the task author read.
func TestMapLabStatus_CarriesTheBootstrapOfEachDevice(t *testing.T) {
	lab := &labpb.Lab{Status: &labpb.LabStatus{Devices: []*labpb.LabDeviceStatus{
		{Name: "plain"},
		{Name: "waiting", Bootstrap: &labpb.BootstrapStatus{Phase: labpb.BootstrapPhase_BOOTSTRAP_PHASE_PENDING, Attempts: 1, Reason: "script exited 3"}},
		{Name: "done", Bootstrap: &labpb.BootstrapStatus{Phase: labpb.BootstrapPhase_BOOTSTRAP_PHASE_DONE, Attempts: 1}},
		{Name: "broken", Bootstrap: &labpb.BootstrapStatus{Phase: labpb.BootstrapPhase_BOOTSTRAP_PHASE_FAILED, Attempts: 3, Reason: "time limit"}},
		{Name: "unseen", Bootstrap: &labpb.BootstrapStatus{Phase: labpb.BootstrapPhase_BOOTSTRAP_PHASE_NONE}},
	}}}
	devices := mapLabStatus(lab).Devices
	require.Len(t, devices, 5)
	require.Nil(t, devices[0].Bootstrap, "a device without a bootstrap shows none")
	require.Equal(t, &exerciseModel.DeviceBootstrapStatus{Phase: "pending", Attempts: 1, Reason: "script exited 3"}, devices[1].Bootstrap)
	require.Equal(t, "done", devices[2].Bootstrap.Phase)
	require.Equal(t, &exerciseModel.DeviceBootstrapStatus{Phase: "failed", Attempts: 3, Reason: "time limit"}, devices[3].Bootstrap)
	require.Nil(t, devices[4].Bootstrap, "nothing observed yet")
}
