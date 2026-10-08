package eventLabModel

import (
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestManualCapabilitiesRequireProgressiveCurrentUnresolvedRetainedState(t *testing.T) {
	at := testNow.Add(time.Minute)
	lab := Lab{Ref: Ref{"team", "lab"}, AgentUID: "uid", AgentGeneration: 1, Revision: 1, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Running", SnapshotMode: "skip"}
	stop, restart := lab.ManualCapabilities(true, true, testNow)
	require.True(t, stop)
	require.False(t, restart)
	stop, restart = lab.ManualCapabilities(false, true, testNow)
	require.False(t, stop)
	require.False(t, restart)
	require.NoError(t, lab.Close("manual", uuid.Must(uuid.NewV7()), testNow))
	lab.RetentionUntil = &at
	lab.ActualState = "Stopped"
	lab.ObservedRevision = lab.Revision
	lab.Allocation = Allocation{RuntimeState: "Released", ReleasedAt: &testNow, StorageState: "Retained"}
	lab.AccessFenced = true
	_, restart = lab.ManualCapabilities(true, true, testNow)
	require.True(t, restart)
	_, restart = lab.ManualCapabilities(true, true, at)
	require.False(t, restart)
	lab.CloseReason = "stage"
	_, restart = lab.ManualCapabilities(true, true, testNow)
	require.False(t, restart)
	lab.CloseReason = "solved"
	stop, restart = lab.ManualCapabilities(true, true, testNow)
	require.False(t, stop)
	require.False(t, restart)
}
func TestDefaultPolicyPreservesExplicitZeroRetentionAndClonesActiveLimit(t *testing.T) {
	previous := DefaultPolicy()
	t.Cleanup(func() { SetDefaultPolicy(previous) })
	n := int32(7)
	SetDefaultPolicy(Policy{SnapshotMode: "required", MaxActiveLabsPerTeam: &n, RetentionMinutes: 0})
	n = 1
	p := DefaultPolicy()
	require.EqualValues(t, 7, *p.MaxActiveLabsPerTeam)
	require.EqualValues(t, 0, p.RetentionMinutes)
	*p.MaxActiveLabsPerTeam = 2
	require.EqualValues(t, 7, *DefaultPolicy().MaxActiveLabsPerTeam)
	_ = time.Second
}
