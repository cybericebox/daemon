package eventLabModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// Unborn reports a lab that was never sent to the agent: no create dispatch and
// no birth identity. Nothing of it exists in the cluster, so nothing there can be
// stopped, observed or released.
func (l Lab) Unborn() bool {
	return l.AgentUID == "" && l.AgentGeneration == 0 && l.CreateEvidence == nil
}

// UnbornStopSettled reports a stopped unborn lab whose stop is already
// acknowledged (the lifecycle no longer chases it).
func (l Lab) UnbornStopSettled() bool {
	return l.DesiredState == "Stopped" && l.Unborn() && l.ActualState == "Stopped" && l.ObservedRevision == l.Revision &&
		l.Allocation.RuntimeState == "Released" && l.Allocation.ReleasedAt != nil && l.AccessFenced && l.FailureCode == "" &&
		(l.SnapshotMode == "skip" || l.SnapshotState == "Succeeded")
}

// SettleUnbornStop acknowledges the stop of a lab closed before it ever reached
// the agent (for example the finish passed while it still waited for the
// reservation): it is stopped and holds nothing, without asking the agent. A
// required final snapshot is met by definition: the lab never ran. False when
// the lab is not an unborn stop or it is already settled.
func (l *Lab) SettleUnbornStop(now time.Time) bool {
	if l.DesiredState != "Stopped" || !l.Unborn() || l.UnbornStopSettled() {
		return false
	}
	l.ActualState = "Stopped"
	l.ObservedRevision = l.Revision
	l.ObservedAt = cloneTime(&now)
	l.ActualStoppedAt = cloneTime(&now)
	l.RuntimeReady = false
	if l.SnapshotMode == "required" {
		l.SnapshotState = "Succeeded"
	}
	l.Allocation.RuntimeState = "Released"
	l.Allocation.ReleasedAt = cloneTime(&now)
	l.Allocation.AllocatedRequests, l.Allocation.Used = Compute{}, Compute{}
	l.AccessFenced, l.AccessFencedAt = true, cloneTime(&now)
	l.FailureCode, l.FailureMessage = "", ""
	l.UpdatedAt = now
	return true
}

// ReopenUnborn returns a lab the event finish closed before it ever reached the
// agent to its first pending state, once the finish moved back into the future:
// revision 1 with a new create operation and an empty ledger, as a lab that was
// just prepared, so the stand engine deploys it on schedule. A lab closed by a
// stage, by hand or by its solve never reopens here.
func (l *Lab) ReopenUnborn(now time.Time) bool {
	if l.DesiredState != "Stopped" || l.CloseReason != "event" || !l.Unborn() || !l.Materialized || l.RetirementStopTarget != nil {
		return false
	}
	l.DesiredState, l.ActualState = "Running", "Unknown"
	l.CloseReason = ""
	l.ClosedAt = nil
	l.Revision, l.ObservedRevision = 1, 0
	l.OperationID = uuid.Must(uuid.NewV7())
	l.RuntimeReady = false
	l.SnapshotState = "Unknown"
	l.ObservedAt, l.ActualStoppedAt, l.ProtectedUntil = nil, nil, nil
	l.Allocation = Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}
	l.AccessFenced, l.AccessFencedAt, l.AccessFenceVPNBootID = false, nil, ""
	l.FailureCode, l.FailureMessage = "", ""
	l.NextAttemptAt, l.UpdatedAt = now, now
	return true
}
