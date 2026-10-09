package eventLabModel

import (
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestHeldComputeRequiresCurrentConfirmedRelease(t *testing.T) {
	at := testNow.Add(time.Minute)
	base := Lab{Ref: Ref{"team", "shared"}, AgentUID: "uid", AgentGeneration: 7, OperationID: uuid.Must(uuid.NewV7()), Revision: 2, DesiredState: "Stopped", ActualState: "Running", SnapshotMode: "required", ObservedAt: &testNow, Allocation: Allocation{AllocatedRequests: Compute{750, 512 << 20}, RuntimeState: "Allocated", StorageState: "Retained", SnapshotQuotaBytes: 1 << 30}}
	valid := Observation{Ref: base.Ref, UID: base.AgentUID, Generation: 7, ObservedGeneration: 7, OperationID: base.OperationID, Revision: 2, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "Succeeded", ObservedAt: &at, AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &at, StorageState: "Retained"}}
	for name, mutate := range map[string]func(*Observation){"wrong UID": func(o *Observation) { o.UID = "other" }, "revision": func(o *Observation) { o.Revision = 1 }, "generation": func(o *Observation) { o.ObservedGeneration = 6 }, "old timestamp": func(o *Observation) { o.ObservedAt = &testNow }, "absent observation": func(o *Observation) { o.ObservedAt = nil }, "Unknown runtime": func(o *Observation) { o.Allocation.RuntimeState = "Unknown" }, "snapshot failed": func(o *Observation) { o.SnapshotState = "Failed" }, "unfenced": func(o *Observation) { o.AccessFenced = false }} {
		t.Run(name, func(t *testing.T) {
			lab := base
			o := valid
			mutate(&o)
			lab.Observe(o, at)
			require.Equal(t, Compute{750, 512 << 20}, lab.HeldCompute())
			require.EqualValues(t, 1<<30, lab.HeldStorage().SnapshotQuotaBytes)
		})
	}
	lab := base
	require.True(t, lab.Observe(valid, at))
	require.Equal(t, Compute{}, lab.HeldCompute())
	require.EqualValues(t, 1<<30, lab.HeldStorage().SnapshotQuotaBytes)
	require.False(t, lab.HeldStorage().PhysicalKnown)
}
func TestHeldComputeDoesNotTrustHistoricalReleasedState(t *testing.T) {
	lab := Lab{Revision: 3, ObservedRevision: 2, ActualState: "Stopped", Allocation: Allocation{AllocatedRequests: Compute{750, 512 << 20}, RuntimeState: "Released", ReleasedAt: &testNow}}
	require.Equal(t, Compute{750, 512 << 20}, lab.HeldCompute())
	lab.ObservedRevision = 3
	lab.AgentUID = "uid"
	lab.AgentGeneration = 1
	lab.ObservedAt = &testNow
	lab.AccessFenced = true
	lab.SnapshotMode = "skip"
	require.Equal(t, Compute{}, lab.HeldCompute())
}
func TestDeletedManifestDoesNotCreditKnownPhysicalStorage(t *testing.T) {
	at := testNow.Add(time.Minute)
	lab := Lab{Ref: Ref{"team", "shared"}, AgentUID: "uid", AgentGeneration: 1, Revision: 3, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Deleted", SnapshotMode: "skip", Allocation: Allocation{AllocatedRequests: Compute{750, 512 << 20}, RuntimeState: "Allocated", StorageState: "Retained", SnapshotQuotaBytes: 1 << 30, PhysicalStorageBytes: 42, PhysicalStorageBytesAvailable: true}}
	o := Observation{Ref: lab.Ref, UID: lab.AgentUID, Generation: 1, ObservedGeneration: 1, Revision: 3, OperationID: lab.OperationID, DesiredState: "Deleted", ActualState: "Deleted", ObservedAt: &at, AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &at, StorageState: "Deleted"}}
	require.True(t, lab.Observe(o, at))
	require.EqualValues(t, 1<<30, lab.Allocation.SnapshotQuotaBytes)
	require.NotEqual(t, "Deleted", lab.Allocation.StorageState)
}

func TestAllocatedZeroCannotErasePendingAdmission(t *testing.T) {
	at := testNow.Add(time.Minute)
	lab := Lab{Ref: Ref{"team", "lab"}, AgentUID: "uid", AgentGeneration: 1, Revision: 1, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Running", Allocation: Allocation{RuntimeState: "Admitted", AllocatedRequests: Compute{750, 512 << 20}, SnapshotQuotaBytes: 1 << 30}}
	o := Observation{Ref: lab.Ref, UID: lab.AgentUID, Generation: 1, ObservedGeneration: 1, Revision: 1, OperationID: lab.OperationID, DesiredState: "Running", ActualState: "Starting", ObservedAt: &at, Allocation: Allocation{RuntimeState: "Allocated"}}
	require.True(t, lab.Observe(o, at))
	require.Equal(t, Compute{750, 512 << 20}, lab.HeldCompute())
	require.EqualValues(t, 1<<30, lab.Allocation.SnapshotQuotaBytes)
}
func TestKnownPhysicalHistoryCannotBeErasedBeforeGC(t *testing.T) {
	at := testNow.Add(time.Minute)
	lab := Lab{Ref: Ref{"team", "lab"}, AgentUID: "uid", AgentGeneration: 1, Revision: 3, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Deleted", SnapshotMode: "skip", Allocation: Allocation{RuntimeState: "Allocated", AllocatedRequests: Compute{750, 512 << 20}, StorageState: "Retained", SnapshotQuotaBytes: 1 << 30, PhysicalStorageBytesAvailable: true, PhysicalStorageBytes: 42}}
	o := Observation{Ref: lab.Ref, UID: lab.AgentUID, Generation: 1, ObservedGeneration: 1, Revision: 3, OperationID: lab.OperationID, DesiredState: "Deleted", ActualState: "Deleting", ObservedAt: &at, Allocation: Allocation{RuntimeState: "Unknown", StorageState: "CleanupPending"}}
	require.True(t, lab.Observe(o, at))
	require.False(t, lab.HeldStorage().PhysicalKnown)
	at = at.Add(time.Minute)
	o.ObservedAt = &at
	o.ActualState = "Deleted"
	o.AccessFenced = true
	o.Allocation.RuntimeState = "Released"
	o.Allocation.ReleasedAt = &at
	o.Allocation.StorageState = "Deleted"
	require.True(t, lab.Observe(o, at))
	require.EqualValues(t, 1<<30, lab.HeldStorage().SnapshotQuotaBytes)
	at = at.Add(time.Minute)
	o.ObservedAt = &at
	o.Allocation.PhysicalStorageBytesAvailable = true
	o.Allocation.PhysicalStorageBytes = 0
	require.True(t, lab.Observe(o, at))
	require.EqualValues(t, 0, lab.HeldStorage().SnapshotQuotaBytes)
	require.True(t, lab.HeldStorage().PhysicalKnown)
}
