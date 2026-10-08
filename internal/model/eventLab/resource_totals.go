package eventLabModel

import "time"

// ResourceTotals is a held ledger, with freshness separate from capacity credit.
// PendingStarts and GroupServices are subsets of Held, never extra demand.
type ResourceTotals struct {
	ObservedAt                         *time.Time
	Complete                           bool
	Held, PendingStarts, GroupServices Compute
	Storage                            StorageBudget
}

func (t *ResourceTotals) AddLab(l Lab, now time.Time) {
	held := l.HeldCompute()
	t.Held.CPUMillicores += held.CPUMillicores
	t.Held.MemoryBytes += held.MemoryBytes
	if l.Allocation.RuntimeState == "Admitted" {
		t.PendingStarts.CPUMillicores += held.CPUMillicores
		t.PendingStarts.MemoryBytes += held.MemoryBytes
	}
	t.Storage.SnapshotQuotaBytes += l.Allocation.SnapshotQuotaBytes
	t.Storage.PhysicalStorageBytes += l.Allocation.PhysicalStorageBytes
	if !l.Allocation.PhysicalStorageBytesAvailable {
		t.Storage.PhysicalKnown = false
	}
	current := l.ActualState != "Unknown" && l.Allocation.ObservedAt != nil && !l.Allocation.ObservedAt.After(now) && now.Sub(*l.Allocation.ObservedAt) <= 30*time.Second && l.AgentUID != "" && l.AgentGeneration > 0 && l.ObservedRevision == l.Revision && l.ObservedAt != nil && !l.ObservedAt.After(now) && now.Sub(*l.ObservedAt) <= 30*time.Second && l.Allocation.RuntimeState != "Unknown" && l.Allocation.RuntimeState != "Admitted"
	if !current {
		t.Complete = false
	}
	if l.ObservedAt != nil && (t.ObservedAt == nil || l.ObservedAt.Before(*t.ObservedAt)) {
		at := *l.ObservedAt
		t.ObservedAt = &at
	}
}
