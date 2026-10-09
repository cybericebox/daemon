package eventLabModel

import (
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGroupStopRequiresEveryCurrentChildAndNoPendingStart(t *testing.T) {
	g := Group{Name: "team", AgentUID: "group-uid", AgentGeneration: 1, Revision: 1, DesiredState: "Running", ConfiguredRequests: Compute{375, 384 << 20}}
	l := Lab{Ref: Ref{"team", "lab"}, AgentUID: "uid", AgentGeneration: 1, Revision: 2, ObservedRevision: 2, DesiredState: "Stopped", ActualState: "Stopped", SnapshotMode: "skip", ObservedAt: &testNow, AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &testNow}}
	for name, change := range map[string]func(*Lab){"unknown": func(l *Lab) { l.ActualState = "Unknown" }, "stopping": func(l *Lab) { l.ActualState = "Stopping" }, "failed": func(l *Lab) { l.ActualState = "StopFailed" }, "wrong revision": func(l *Lab) { l.ObservedRevision = 1 }, "capture": func(l *Lab) { l.SnapshotMode = "required"; l.SnapshotState = "Failed" }} {
		t.Run(name, func(t *testing.T) {
			candidate := g
			child := l
			change(&child)
			require.False(t, candidate.RequestStop([]Lab{child}, uuid.Must(uuid.NewV7()), false, testNow))
		})
	}
	pending := g
	pending.PendingStarts = 1
	require.False(t, pending.RequestStop([]Lab{l}, uuid.Must(uuid.NewV7()), false, testNow))
	require.False(t, g.RequestStop([]Lab{l}, uuid.Must(uuid.NewV7()), true, testNow))
	require.True(t, g.RequestStop([]Lab{l}, uuid.Must(uuid.NewV7()), false, testNow))
	require.EqualValues(t, 2, g.Revision)
	require.Equal(t, Compute{375, 384 << 20}, g.HeldCompute())
	at := testNow.Add(time.Minute)
	o := GroupObservation{Name: "team", UID: "group-uid", Generation: 1, ObservedGeneration: 1, OperationID: g.OperationID, Revision: g.Revision, DesiredState: "Stopped", ActualState: "Stopped", ObservedAt: &at, AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &at}}
	require.True(t, g.Observe(o, at))
	require.Equal(t, Compute{}, g.HeldCompute())
	require.True(t, g.RequestRunning(1, uuid.Must(uuid.NewV7()), at))
	require.Equal(t, Compute{375, 384 << 20}, g.HeldCompute())
	require.False(t, g.AllowsChildren(at))
}
func TestGroupStartCannotGrantChildrenBeforeExactReadiness(t *testing.T) {
	g := Group{Name: "team", AgentUID: "uid", AgentGeneration: 2, Revision: 2, ObservedRevision: 2, DesiredState: "Stopped", ActualState: "Stopped", AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &testNow}, ConfiguredRequests: Compute{375, 384 << 20}}
	require.True(t, g.RequestRunning(1, uuid.Must(uuid.NewV7()), testNow))
	require.False(t, g.AllowsChildren(testNow))
	at := testNow.Add(time.Second)
	o := GroupObservation{Name: "team", UID: "uid", OperationID: g.OperationID, Revision: g.Revision, Generation: 3, ObservedGeneration: 3, DesiredState: "Running", ActualState: "Running", Ready: true, ObservedAt: &at, Allocation: Allocation{RuntimeState: "Allocated", AllocatedRequests: Compute{375, 384 << 20}}}
	stale := o
	stale.Revision--
	require.False(t, g.Observe(stale, at))
	require.False(t, g.AllowsChildren(at))
	require.True(t, g.Observe(o, at))
	require.True(t, g.AllowsChildren(at))
	require.False(t, g.AllowsChildren(at.Add(time.Minute)))
}
