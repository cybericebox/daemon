package eventLabModel

import (
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func stoppedForRetirement() Lab {
	at := testNow.Add(time.Minute)
	return Lab{ID: uuid.Must(uuid.NewV7()), Ref: Ref{"team", "lab"}, AgentUID: "uid", AgentGeneration: 7, Revision: 2, ObservedRevision: 2, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Stopped", ActualState: "Stopped", SnapshotMode: "required", SnapshotState: "Succeeded", AccessFenced: true, ObservedAt: &testNow, RetentionUntil: &at, Allocation: Allocation{ConfiguredRequests: Compute{750, 512 << 20}, AllocatedRequests: Compute{750, 512 << 20}, RuntimeState: "Released", ReleasedAt: &testNow, StorageState: "Retained", SnapshotQuotaBytes: 1 << 30}}
}
func TestRetirementDeadlineAndAcceptanceNeverReleaseStorage(t *testing.T) {
	for name, delta := range map[string]time.Duration{"before": -time.Microsecond, "at": 0, "after": time.Microsecond} {
		t.Run(name, func(t *testing.T) {
			l := stoppedForRetirement()
			at := *l.RetentionUntil
			accepted := l.RequestRetirement(uuid.Must(uuid.NewV7()), at.Add(delta))
			require.Equal(t, delta >= 0, accepted)
			require.EqualValues(t, 1<<30, l.HeldStorage().SnapshotQuotaBytes)
			if accepted {
				require.Equal(t, "Stopped", l.ActualState)
				require.Equal(t, "CleanupPending", l.Allocation.StorageState)
				require.Equal(t, Compute{}, l.HeldCompute())
			}
		})
	}
}
func TestRetirementRequiresIndependentExactFreshCleanupCertificate(t *testing.T) {
	l := stoppedForRetirement()
	deadline := *l.RetentionUntil
	require.True(t, l.RequestRetirement(uuid.Must(uuid.NewV7()), deadline))
	requestAt := deadline.Add(time.Second)
	observedAt := requestAt.Add(time.Second)
	o := Observation{Ref: l.Ref, UID: l.AgentUID, OperationID: l.RetirementStopTarget.OperationID, Revision: l.RetirementStopTarget.Revision, Generation: 7, ObservedGeneration: 7, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "Succeeded", AccessFenced: true, ObservedAt: &testNow, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &testNow}, Retirement: &RetirementObservation{ExpectedUID: l.AgentUID, StopOperationID: l.RetirementStopTarget.OperationID, StopRevision: l.RetirementStopTarget.Revision, OperationID: l.OperationID, Revision: l.Revision, ObservedGeneration: 7, State: "Deleted", RequestedAt: &requestAt, ObservedAt: &observedAt, RuntimeAbsent: true, StorageState: "Deleted", CleanupComplete: true}}
	for name, change := range map[string]func(*RetirementObservation){"wrong UID": func(r *RetirementObservation) { r.ExpectedUID = "other" }, "old stop": func(r *RetirementObservation) { r.StopRevision-- }, "old retirement": func(r *RetirementObservation) { r.Revision-- }, "wrong operation": func(r *RetirementObservation) { r.OperationID = uuid.Nil }, "generation": func(r *RetirementObservation) { r.ObservedGeneration = 6 }, "not fresh": func(r *RetirementObservation) { r.ObservedAt = &requestAt }, "runtime alive": func(r *RetirementObservation) { r.RuntimeAbsent = false }, "acceptance only": func(r *RetirementObservation) { r.CleanupComplete = false }, "error": func(r *RetirementObservation) { r.Error = "GC failed" }} {
		t.Run(name, func(t *testing.T) {
			candidate := l
			receipt := *o.Retirement
			change(&receipt)
			sample := o
			sample.Retirement = &receipt
			candidate.Observe(sample, observedAt)
			require.EqualValues(t, 1<<30, candidate.HeldStorage().SnapshotQuotaBytes)
			require.NotEqual(t, "Deleted", candidate.ActualState)
		})
	}
	require.True(t, l.Observe(o, observedAt))
	require.Equal(t, "Deleted", l.ActualState)
	require.EqualValues(t, 0, l.HeldStorage().SnapshotQuotaBytes)
	require.False(t, l.HeldStorage().PhysicalKnown)
}
func TestKnownPhysicalRetirementNeedsCurrentKnownZero(t *testing.T) {
	l := stoppedForRetirement()
	l.Allocation.PhysicalStorageBytes = 42
	l.Allocation.PhysicalStorageBytesAvailable = true
	l.Allocation.PhysicalStorageEverKnown = true
	require.True(t, l.RequestRetirement(uuid.Must(uuid.NewV7()), *l.RetentionUntil))
	requested := testNow.Add(2 * time.Minute)
	at := requested.Add(time.Second)
	o := Observation{Ref: l.Ref, UID: l.AgentUID, OperationID: l.RetirementStopTarget.OperationID, Revision: l.RetirementStopTarget.Revision, Generation: 7, ObservedGeneration: 7, ActualState: "Stopped", SnapshotState: "Succeeded", AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &testNow}, Retirement: &RetirementObservation{ExpectedUID: l.AgentUID, StopOperationID: l.RetirementStopTarget.OperationID, StopRevision: l.RetirementStopTarget.Revision, OperationID: l.OperationID, Revision: l.Revision, ObservedGeneration: 7, State: "Deleted", RequestedAt: &requested, ObservedAt: &at, RuntimeAbsent: true, CleanupComplete: true, StorageState: "Deleted"}}
	require.True(t, l.ObserveRetirement(o, at))
	require.EqualValues(t, 1<<30, l.HeldStorage().SnapshotQuotaBytes)
	at = at.Add(time.Second)
	o.Retirement.ObservedAt = &at
	o.Retirement.PhysicalStorageBytesAvailable = true
	o.Retirement.PhysicalStorageBytes = 0
	require.True(t, l.ObserveRetirement(o, at))
	require.EqualValues(t, 0, l.HeldStorage().SnapshotQuotaBytes)
	require.True(t, l.HeldStorage().PhysicalKnown)
}
func TestKnownZeroDefinitionStillHoldsRuntimeAndUnknownZeroCannotLaunch(t *testing.T) {
	l := Lab{DesiredState: "Running", Generation: 4, DefinitionVersionID: uuid.Must(uuid.NewV7()), DefinitionHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	require.False(t, l.Admit(Compute{}, 0, testNow))
	require.False(t, l.AdmitKnown(Compute{}, 0, "wrong", 4, testNow))
	require.False(t, l.AdmitKnown(Compute{}, 0, l.DefinitionHash, 3, testNow))
	require.True(t, l.AdmitKnown(Compute{}, 0, l.DefinitionHash, 4, testNow))
	require.True(t, l.HoldsRuntime())
	require.Equal(t, Compute{}, l.HeldCompute())
	require.True(t, l.Allocation.ConfiguredRequestsKnown)
	require.Equal(t, "Admitted", l.Allocation.RuntimeState)
}
