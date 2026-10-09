package eventLabModel

import (
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"testing"
)

func TestRequiredPolicyNeedsConfiguredCapture(t *testing.T) {
	p := Policy{SnapshotMode: "required", RetentionMinutes: 60}
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "container", Type: exerciseModel.DeviceTypeContainer}}}
	if p.ValidatePreparation(topology, true) == nil {
		t.Fatal("accepted missing persistence")
	}
	topology.Devices[0].Persistence = &exerciseModel.DevicePersistence{Enabled: true}
	if p.ValidatePreparation(topology, false) == nil {
		t.Fatal("accepted absent capture capability")
	}
	if err := p.ValidatePreparation(topology, true); err != nil {
		t.Fatal(err)
	}
	topology.Devices = []exerciseModel.Device{{Type: exerciseModel.DeviceTypeHub}}
	if err := p.ValidatePreparation(topology, true); err != nil {
		t.Fatal(err)
	}
}
func TestPolicyBounds(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	n := int32(0)
	for _, p := range []Policy{{SnapshotMode: ""}, {SnapshotMode: "skip", RetentionMinutes: -1}, {SnapshotMode: "skip", RetentionMinutes: 10081}, {SnapshotMode: "skip", MaxActiveLabsPerTeam: &n}} {
		if p.Validate() == nil {
			t.Fatal("accepted", p)
		}
	}
}
