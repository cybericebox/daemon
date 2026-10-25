package eventLabModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

func unbornClosedByFinish(t *testing.T, now time.Time) Lab {
	t.Helper()
	l, err := New(NewInput{EventID: uuid.Must(uuid.NewV7()), TeamID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()),
		Ref: Ref{Group: "e-a-t-b", Lab: "x-c-v0"}, ObjectiveIDs: []uuid.UUID{uuid.Must(uuid.NewV7())}, Policy: Policy{SnapshotMode: "skip", RetentionMinutes: 60}}, now)
	if err != nil {
		t.Fatal(err)
	}
	l.Materialized = true
	if err = l.Close("event", uuid.Must(uuid.NewV7()), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return l
}

// Production 2026-10-25: the schedule made an event run for two minutes; its labs were prepared,
// never sent to the agent, and closed by the finish. Their stop could never be acknowledged.
func TestUnbornStopIsSettledWithoutTheAgent(t *testing.T) {
	now := time.Date(2026, 10, 25, 8, 33, 5, 0, time.UTC)
	l := unbornClosedByFinish(t, now)
	if !l.Unborn() || l.UnbornStopSettled() {
		t.Fatalf("closed unborn lab: %+v", l)
	}
	if !l.SettleUnbornStop(now.Add(3 * time.Minute)) {
		t.Fatal("an unborn stop is settled")
	}
	if !l.UnbornStopSettled() || l.HeldCompute() != (Compute{}) || l.ObservedRevision != l.Revision {
		t.Fatalf("settled lab: %+v", l)
	}
	if l.SettleUnbornStop(now.Add(4 * time.Minute)) {
		t.Fatal("settling twice is a change")
	}
	required := unbornClosedByFinish(t, now)
	required.SnapshotMode = "required"
	if !required.SettleUnbornStop(now) || !required.UnbornStopSettled() {
		t.Fatalf("a lab that never ran has nothing to capture: %+v", required)
	}
	born := unbornClosedByFinish(t, now)
	born.AgentUID, born.AgentGeneration = "uid", 1
	if born.SettleUnbornStop(now) {
		t.Fatal("a lab the agent holds is stopped through the agent")
	}
	running := Lab{DesiredState: "Running"}
	if running.SettleUnbornStop(now) {
		t.Fatal("only a stop is settled")
	}
}

func TestReopenUnbornReturnsTheFirstPendingState(t *testing.T) {
	now := time.Date(2026, 10, 25, 8, 33, 5, 0, time.UTC)
	l := unbornClosedByFinish(t, now)
	l.SettleUnbornStop(now.Add(3 * time.Minute))
	closedOperation := l.OperationID
	if !l.ReopenUnborn(now.Add(15 * time.Minute)) {
		t.Fatal("an unborn lab the finish closed reopens")
	}
	if l.DesiredState != "Running" || l.CloseReason != "" || l.ClosedAt != nil || l.Revision != 1 || l.ObservedRevision != 0 || l.OperationID == closedOperation || l.AccessFenced {
		t.Fatalf("reopened lab: %+v", l)
	}
	// It deploys again as a just prepared lab: admission and the first create dispatch work.
	if !l.Admit(Compute{CPUMillicores: 500, MemoryBytes: 1 << 20}, 0, now) || !l.RecordCreateDispatch("group-uid", "hash", l.OperationID, now) {
		t.Fatalf("reopened lab cannot deploy: %+v", l)
	}
	for _, reason := range []string{"manual", "stage", "solved"} {
		other := unbornClosedByFinish(t, now)
		other.CloseReason = reason
		if other.ReopenUnborn(now) {
			t.Fatalf("a lab closed by %q does not reopen", reason)
		}
	}
	born := unbornClosedByFinish(t, now)
	born.AgentUID, born.AgentGeneration = "uid", 1
	if born.ReopenUnborn(now) {
		t.Fatal("a lab the agent knows does not reopen this way")
	}
}
