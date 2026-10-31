package eventLabModel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func node(name string, persistent bool) exerciseModel.Device {
	d := exerciseModel.Device{Name: name, Type: exerciseModel.DeviceTypeNode}
	if persistent {
		d.Persistence = &exerciseModel.DevicePersistence{Enabled: true}
	}
	return d
}

// A lab with a device that keeps state is stopped with a snapshot whatever the event's default; the other devices of
// the lab need no snapshot and do not stop the lab from being prepared.
func TestPolicyForTopology(t *testing.T) {
	skip := eventLabModel.Policy{SnapshotMode: "skip", RetentionMinutes: 60}
	plain := exerciseModel.Topology{Devices: []exerciseModel.Device{node("web", false)}}
	mixed := exerciseModel.Topology{Devices: []exerciseModel.Device{node("web", false), node("db", true)}}

	assert.Equal(t, "skip", skip.ForTopology(plain).SnapshotMode, "no persistent device: as the event says")
	assert.Equal(t, "skip", skip.ForTopology(exerciseModel.Topology{}).SnapshotMode)

	required := skip.ForTopology(mixed)
	assert.Equal(t, "required", required.SnapshotMode)
	assert.Equal(t, int32(60), required.RetentionMinutes)
	require.NoError(t, required.ValidatePreparation(mixed, true), "devices without persistence do not refuse the lab")
	require.ErrorIs(t, required.ValidatePreparation(mixed, false), eventLabModel.ErrCaptureUnavailable.Err(), "an agent that cannot capture refuses it")
	assert.Equal(t, "skip", skip.SnapshotMode, "the event's policy is not changed")

	strict := eventLabModel.Policy{SnapshotMode: "required"}
	require.ErrorIs(t, strict.ForTopology(mixed).ValidatePreparation(mixed, true), eventLabModel.ErrCaptureUnavailable.Err(), "an explicit required policy keeps its own rule")
}
