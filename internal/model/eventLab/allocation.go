package eventLabModel

import "time"

type Compute struct{ CPUMillicores, MemoryBytes int64 }

// Allocation separates configured demand, held compute and retained storage.
// Unknown observations must never be converted into free capacity.
type Allocation struct {
	ConfiguredRequests, ConfiguredLimits, AllocatedRequests, Used Compute
	RuntimeState, StorageState                                    string
	ObservedAt, ReleasedAt                                        *time.Time
	UsageAvailable                                                bool
	SnapshotQuotaBytes, PhysicalStorageBytes                      int64
	PhysicalStorageBytesAvailable                                 bool
	PhysicalStorageEverKnown                                      bool
}

// mergeAllocation keeps the last held ledger when monitoring has no proof.
// Runtime and retained storage have separate confirmation requirements.
func (l *Lab) mergeAllocation(o Observation) Allocation {
	current := l.Allocation
	next := current
	release := o.Allocation.RuntimeState == "Released" && l.DesiredState != "Running" &&
		(o.ActualState == "Stopped" || o.ActualState == "Deleted") && o.FailureCode == "" &&
		o.AccessFenced && o.Allocation.ReleasedAt != nil &&
		(l.SnapshotMode != "required" || o.SnapshotState == "Succeeded")
	allocated := (o.Allocation.RuntimeState == "Allocated" || o.Allocation.RuntimeState == "Releasing") &&
		o.Allocation.AllocatedRequests.CPUMillicores >= 0 && o.Allocation.AllocatedRequests.MemoryBytes >= 0
	if release || allocated {
		next = o.Allocation
		next.ObservedAt = cloneTime(o.Allocation.ObservedAt)
		next.ReleasedAt = cloneTime(o.Allocation.ReleasedAt)
		if release {
			next.AllocatedRequests = current.AllocatedRequests
		} else {
			next.AllocatedRequests = Compute{max(current.AllocatedRequests.CPUMillicores, next.AllocatedRequests.CPUMillicores), max(current.AllocatedRequests.MemoryBytes, next.AllocatedRequests.MemoryBytes)}
		}
		if !release {
			next.ReleasedAt = nil
		}
	} else {
		next.UsageAvailable = false
	}
	// Demand belongs to the preserved definition. A partial/missing measurement
	// cannot erase configured requests/limits already known for this generation.
	if next.ConfiguredRequests == (Compute{}) {
		next.ConfiguredRequests = current.ConfiguredRequests
	}
	if next.ConfiguredLimits == (Compute{}) {
		next.ConfiguredLimits = current.ConfiguredLimits
	}
	next.StorageState = current.StorageState
	next.SnapshotQuotaBytes = current.SnapshotQuotaBytes
	next.PhysicalStorageBytes = current.PhysicalStorageBytes
	next.PhysicalStorageBytesAvailable = false
	next.PhysicalStorageEverKnown = current.PhysicalStorageEverKnown || current.PhysicalStorageBytesAvailable
	switch o.Allocation.StorageState {
	case "Retained", "DeleteRequested", "CleanupPending", "None":
		next.StorageState = o.Allocation.StorageState
		// A retention observation cannot silently relinquish the configured quota.
		if o.Allocation.SnapshotQuotaBytes > next.SnapshotQuotaBytes {
			next.SnapshotQuotaBytes = o.Allocation.SnapshotQuotaBytes
		}
	case "Deleted":
		if release && l.DesiredState == "Deleted" && o.ActualState == "Deleted" && ((!next.PhysicalStorageEverKnown && current.PhysicalStorageBytes == 0) || (o.Allocation.PhysicalStorageBytesAvailable && o.Allocation.PhysicalStorageBytes == 0)) {
			next.StorageState = "Deleted"
			next.SnapshotQuotaBytes = 0
		}
	}
	if o.Allocation.PhysicalStorageBytesAvailable && o.Allocation.PhysicalStorageBytes >= 0 {
		next.PhysicalStorageBytes = o.Allocation.PhysicalStorageBytes
		next.PhysicalStorageBytesAvailable = true
		next.PhysicalStorageEverKnown = true
	}
	return next
}

// StorageBudget reports logical reservation separately from physical measurement.
type StorageBudget struct {
	SnapshotQuotaBytes   int64
	PhysicalStorageBytes int64
	PhysicalKnown        bool
}

func (l *Lab) HeldCompute() Compute {
	if l.DesiredState != "Running" && l.AgentUID != "" && l.AgentGeneration > 0 && l.ObservedAt != nil && l.ObservedRevision == l.Revision && (l.ActualState == "Stopped" || l.ActualState == "Deleted") && l.Allocation.RuntimeState == "Released" && l.Allocation.ReleasedAt != nil && l.AccessFenced && l.FailureCode == "" && (l.SnapshotMode == "skip" || l.SnapshotState == "Succeeded") {
		return Compute{}
	}
	return l.Allocation.AllocatedRequests
}
func (l *Lab) HeldStorage() StorageBudget {
	return StorageBudget{l.Allocation.SnapshotQuotaBytes, l.Allocation.PhysicalStorageBytes, l.Allocation.PhysicalStorageBytesAvailable}
}

// Admit reserves configured demand before dispatch. Retries cannot erase or
// reduce the held ledger; an unresolved prior start remains admitted.
func (l *Lab) Admit(need Compute, storageBytes int64, now time.Time) bool {
	if l.DesiredState != "Running" || l.ClosedAt != nil || need.CPUMillicores <= 0 || need.MemoryBytes <= 0 || storageBytes < 0 {
		return false
	}
	l.Allocation.ConfiguredRequests = need
	l.Allocation.ConfiguredLimits = need
	l.Allocation.AllocatedRequests = Compute{max(l.Allocation.AllocatedRequests.CPUMillicores, need.CPUMillicores), max(l.Allocation.AllocatedRequests.MemoryBytes, need.MemoryBytes)}
	l.Allocation.SnapshotQuotaBytes = max(l.Allocation.SnapshotQuotaBytes, storageBytes)
	l.Allocation.RuntimeState = "Admitted"
	l.Allocation.ReleasedAt = nil
	l.UpdatedAt = now
	return true
}
