package eventLabModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// The cluster deletes everything under a reservation at its end (the reservation ended, or its part was deleted):
// the labs and groups are gone with their snapshots. That end is recorded as a settled terminal state, so the
// lifecycle never chases the gone objects (no start, no stop, no failure) and they hold nothing in the ledger. It is
// told apart from a retirement by having no retirement target.

// EndedWithReservation reports a lab the cluster deleted with its reservation.
func (l *Lab) EndedWithReservation() bool {
	return l.DesiredState == "Deleted" && l.ActualState == "Deleted" && l.RetirementState == "Deleted" && l.RetirementStopTarget == nil
}

// EndWithReservation records that the cluster deleted the lab with its reservation; false when it was already
// recorded or the lab is being retired the normal way. A solved lab keeps its reason.
func (l *Lab) EndWithReservation(operation uuid.UUID, now time.Time) bool {
	if l.EndedWithReservation() || l.RetirementStopTarget != nil || operation == uuid.Nil {
		return false
	}
	l.Revision++
	l.ObservedRevision = l.Revision
	l.OperationID = operation
	l.DesiredState, l.ActualState = "Deleted", "Deleted"
	l.RetirementState, l.RetirementError, l.RetirementObservedAt = "Deleted", "", cloneTime(&now)
	if l.CloseReason != "solved" {
		l.CloseReason = "event"
	}
	if l.ClosedAt == nil {
		l.ClosedAt = cloneTime(&now)
	}
	l.RuntimeReady = false
	l.SnapshotState = "Deleted"
	l.Allocation.RuntimeState, l.Allocation.StorageState = "Released", "Deleted"
	l.Allocation.ReleasedAt = cloneTime(&now)
	l.Allocation.AllocatedRequests, l.Allocation.Used = Compute{}, Compute{}
	l.Allocation.SnapshotQuotaBytes, l.Allocation.PhysicalStorageBytes = 0, 0
	l.AccessFenced, l.AccessFencedAt = true, cloneTime(&now)
	l.FailureCode, l.FailureMessage = "", ""
	l.ObservedAt = cloneTime(&now)
	l.NextAttemptAt, l.UpdatedAt = now, now
	return true
}

// EndedWithReservation reports a group the cluster deleted with its reservation.
func (g *Group) EndedWithReservation() bool {
	return g.DesiredState == "Deleted" && g.ActualState == "Deleted" && g.RetirementState == "Deleted" && g.RetirementStopTarget == nil
}

// EndWithReservation records that the cluster deleted the group with its reservation; false when it was already
// recorded or the group is being retired the normal way.
func (g *Group) EndWithReservation(operation uuid.UUID, now time.Time) bool {
	if g.EndedWithReservation() || g.RetirementStopTarget != nil || operation == uuid.Nil {
		return false
	}
	g.Revision++
	g.ObservedRevision = g.Revision
	g.OperationID = operation
	g.DesiredState, g.ActualState = "Deleted", "Deleted"
	g.RetirementState, g.RetirementError, g.RetirementObservedAt = "Deleted", "", cloneTime(&now)
	g.Ready, g.PendingStarts = false, 0
	g.Allocation.RuntimeState, g.Allocation.StorageState = "Released", "Deleted"
	g.Allocation.ReleasedAt = cloneTime(&now)
	g.Allocation.AllocatedRequests, g.Allocation.Used = Compute{}, Compute{}
	g.ConfiguredRequests = Compute{}
	g.AccessFenced = true
	g.FailureCode, g.FailureMessage = "", ""
	g.ObservedAt = cloneTime(&now)
	g.NextAttemptAt, g.UpdatedAt = now, now
	return true
}
