package eventLabModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

func TestEndWithReservationSettlesLabAndGroup(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	l := Lab{DesiredState: "Running", ActualState: "Running", Revision: 3, ObservedRevision: 3, AgentUID: "uid", AgentGeneration: 1, SnapshotMode: "required",
		Allocation: Allocation{RuntimeState: "Allocated", AllocatedRequests: Compute{CPUMillicores: 500, MemoryBytes: 1 << 20}}}
	if l.HeldCompute() == (Compute{}) {
		t.Fatal("a running lab holds compute")
	}
	op := uuid.Must(uuid.NewV7())
	if !l.EndWithReservation(op, now) || !l.EndedWithReservation() {
		t.Fatal("not ended")
	}
	if l.HeldCompute() != (Compute{}) || l.HeldStorage().SnapshotQuotaBytes != 0 || l.Revision != 4 || l.ObservedRevision != 4 || l.CloseReason != "event" || l.FailureCode != "" {
		t.Fatalf("ended lab: %+v", l)
	}
	if l.EndWithReservation(uuid.Must(uuid.NewV7()), now) {
		t.Fatal("ending twice is a change")
	}
	solved := Lab{DesiredState: "Stopped", CloseReason: "solved", Revision: 2}
	solved.EndWithReservation(op, now)
	if solved.CloseReason != "solved" {
		t.Fatal("a solved lab keeps its reason")
	}
	retiring := Lab{DesiredState: "Deleted", RetirementStopTarget: &Target{}}
	if retiring.EndWithReservation(op, now) {
		t.Fatal("a normal retirement is not taken over")
	}
	g := Group{DesiredState: "Running", Revision: 2, Allocation: Allocation{AllocatedRequests: Compute{CPUMillicores: 100, MemoryBytes: 1}}, ConfiguredRequests: Compute{CPUMillicores: 100, MemoryBytes: 1}}
	if !g.EndWithReservation(op, now) || !g.EndedWithReservation() || g.HeldCompute() != (Compute{}) {
		t.Fatalf("ended group: %+v", g)
	}
}

// A manually stopped lab restarts after its retention deadline too: labs live until the reservation window ends.
func TestRestartIgnoresRetentionDeadline(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	l := Lab{ID: uuid.Must(uuid.NewV7()), Ref: Ref{Group: "team", Lab: "lab"}, DesiredState: "Stopped", ActualState: "Stopped", CloseReason: "manual", ClosedAt: &past,
		AgentUID: "uid", AgentGeneration: 1, Revision: 2, ObservedRevision: 2, ObservedAt: &past, AccessFenced: true, SnapshotMode: "skip",
		RetentionUntil: &past, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &past, StorageState: "Retained"}, OperationID: uuid.Must(uuid.NewV7())}
	if err := l.Start(uuid.Must(uuid.NewV7()), now); err != nil {
		t.Fatalf("restart refused after the retention deadline: %v", err)
	}
	if l.DesiredState != "Running" {
		t.Fatal("not restarted")
	}
}
