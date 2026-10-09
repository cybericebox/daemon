package eventLabModel

import (
	"github.com/gofrs/uuid"
	"time"
)

type RetirementRequest struct {
	StopTarget  Target
	OperationID uuid.UUID
	Revision    int64
}
type GroupRetirementRequest struct {
	StopTarget  GroupTarget
	OperationID uuid.UUID
	Revision    int64
}
type RetirementObservation struct {
	ExpectedUID                                                   string
	StopOperationID                                               uuid.UUID
	StopRevision                                                  int64
	OperationID                                                   uuid.UUID
	Revision, ObservedGeneration                                  int64
	State, StorageState, Error                                    string
	ObservedAt, RequestedAt                                       *time.Time
	RuntimeAbsent, CleanupComplete, PhysicalStorageBytesAvailable bool
	PhysicalStorageBytes                                          int64
}

func (r *RetirementObservation) Matches(uid string, stopOperation uuid.UUID, stopRevision int64, operation uuid.UUID, revision, generation int64, previous *time.Time) bool {
	return r != nil && uid != "" && stopOperation != uuid.Nil && stopRevision > 0 && operation != uuid.Nil && revision > 0 && generation > 0 && r.ExpectedUID == uid && r.StopOperationID == stopOperation && r.StopRevision == stopRevision && r.OperationID == operation && r.Revision == revision && r.ObservedGeneration == generation && r.ObservedAt != nil && r.RequestedAt != nil && r.ObservedAt.After(*r.RequestedAt) && (previous == nil || r.ObservedAt.After(*previous))
}
func (l *Lab) EffectiveRetentionUntil() *time.Time {
	deadline := l.RetentionUntil
	if l.ProtectedUntil != nil && (deadline == nil || l.ProtectedUntil.After(*deadline)) {
		deadline = l.ProtectedUntil
	}
	return cloneTime(deadline)
}
func (l *Lab) RequestRetirement(operation uuid.UUID, now time.Time) bool {
	deadline := l.EffectiveRetentionUntil()
	if l.DesiredState != "Stopped" || deadline == nil || now.Before(*deadline) || l.ActualState != "Stopped" || l.HeldCompute() != (Compute{}) || l.ObservedRevision != l.Revision || l.AgentUID == "" || l.Allocation.RuntimeState != "Released" || !l.AccessFenced || operation == uuid.Nil {
		return false
	}
	target := Target{Ref: l.Ref, ExpectedUID: l.AgentUID, OperationID: l.OperationID, Revision: l.Revision}
	l.RetirementStopTarget = &target
	l.DesiredState = "Deleted"
	l.Revision++
	l.OperationID = operation
	l.RetirementState = "CleanupPending"
	l.Allocation.StorageState = "CleanupPending"
	l.RuntimeReady = false
	l.UpdatedAt = now
	l.NextAttemptAt = now
	return true
}
func (l *Lab) ObserveRetirement(o Observation, now time.Time) bool {
	target := l.RetirementStopTarget
	if l.DesiredState != "Deleted" || target == nil || o.Ref != target.Ref || o.UID != target.ExpectedUID || o.OperationID != target.OperationID || o.Revision != target.Revision || o.Generation < l.AgentGeneration || o.ObservedGeneration != o.Generation || o.ActualState != "Stopped" || o.Allocation.RuntimeState != "Released" || o.Allocation.ReleasedAt == nil || !o.AccessFenced || o.FailureCode != "" || o.FailureMessage != "" || (l.SnapshotMode == "required" && o.SnapshotState != "Succeeded") || !o.Retirement.Matches(l.AgentUID, target.OperationID, target.Revision, l.OperationID, l.Revision, o.Generation, l.RetirementObservedAt) {
		return false
	}
	r := o.Retirement
	l.RetirementObservedAt = cloneTime(r.ObservedAt)
	l.RetirementState = r.State
	l.RetirementError = r.Error
	l.AgentGeneration = o.Generation
	l.UpdatedAt = now
	if r.State == "Deleted" && r.Error == "" && r.RuntimeAbsent && r.CleanupComplete && r.StorageState == "Deleted" && (!l.Allocation.PhysicalStorageEverKnown && !l.Allocation.PhysicalStorageBytesAvailable && l.Allocation.PhysicalStorageBytes == 0 || r.PhysicalStorageBytesAvailable && r.PhysicalStorageBytes == 0) {
		l.ActualState = "Deleted"
		l.ObservedRevision = l.Revision
		l.Allocation.StorageState = "Deleted"
		l.Allocation.SnapshotQuotaBytes = 0
		l.Allocation.ObservedAt = cloneTime(r.ObservedAt)
	} else {
		l.RetirementState = "CleanupPending"
		l.Allocation.StorageState = "CleanupPending"
	}
	if r.PhysicalStorageBytesAvailable && r.PhysicalStorageBytes >= 0 {
		l.Allocation.PhysicalStorageBytesAvailable = true
		l.Allocation.PhysicalStorageEverKnown = true
		l.Allocation.PhysicalStorageBytes = r.PhysicalStorageBytes
	} else {
		l.Allocation.PhysicalStorageBytesAvailable = false
	}
	return true
}
func (g *Group) RequestRetirement(operation uuid.UUID, now time.Time) bool {
	deadline := g.RetentionUntil
	if g.ProtectedUntil != nil && (deadline == nil || g.ProtectedUntil.After(*deadline)) {
		deadline = g.ProtectedUntil
	}
	if g.DesiredState != "Stopped" || deadline == nil || now.Before(*deadline) || g.ActualState != "Stopped" || g.HeldCompute() != (Compute{}) || g.PendingStarts != 0 || operation == uuid.Nil {
		return false
	}
	target := g.Target()
	g.RetirementStopTarget = &target
	g.DesiredState = "Deleted"
	g.Revision++
	g.OperationID = operation
	g.RetirementState = "CleanupPending"
	g.Allocation.StorageState = "CleanupPending"
	g.UpdatedAt = now
	return true
}
func (g *Group) ObserveRetirement(o GroupObservation, now time.Time) bool {
	target := g.RetirementStopTarget
	if target == nil || o.Name != target.Group || o.UID != target.ExpectedUID || o.OperationID != target.OperationID || o.Revision != target.Revision || o.Generation < g.AgentGeneration || o.ObservedGeneration != o.Generation || o.ActualState != "Stopped" || o.Allocation.RuntimeState != "Released" || o.Allocation.ReleasedAt == nil || !o.AccessFenced || o.FailureCode != "" || o.FailureMessage != "" || !o.Retirement.Matches(g.AgentUID, target.OperationID, target.Revision, g.OperationID, g.Revision, o.Generation, g.RetirementObservedAt) {
		return false
	}
	r := o.Retirement
	g.RetirementObservedAt = cloneTime(r.ObservedAt)
	g.RetirementError = r.Error
	g.RetirementState = "CleanupPending"
	g.AgentGeneration = o.Generation
	g.UpdatedAt = now
	if r.State == "Deleted" && r.Error == "" && r.RuntimeAbsent && r.CleanupComplete && r.StorageState == "Deleted" {
		g.RetirementState = "Deleted"
		g.ActualState = "Deleted"
		g.ObservedRevision = g.Revision
		g.Allocation.StorageState = "Deleted"
		g.Allocation.SnapshotQuotaBytes = 0
		g.Allocation.ObservedAt = cloneTime(r.ObservedAt)
	}
	return true
}
