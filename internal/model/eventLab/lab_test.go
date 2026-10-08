package eventLabModel

import (
	"errors"
	"github.com/gofrs/uuid"
	"reflect"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func labInput() NewInput {
	return NewInput{EventID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000001")), TeamID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000002")), EventExerciseID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000003")), Ref: Ref{Group: "team", Lab: "exercise"}, ObjectiveIDs: []uuid.UUID{uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000004"))}, Policy: DefaultPolicy()}
}
func TestSolvedLabCannotRestart(t *testing.T) {
	lab := Lab{ID: uuid.Must(uuid.NewV7()), Revision: 1, DesiredState: "Running"}
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	if lab.Revision != 2 || lab.ClosedAt == nil || lab.CloseReason != "solved" {
		t.Fatal(lab)
	}
	if err := lab.Start(uuid.Must(uuid.NewV7()), testNow.Add(time.Minute)); !errors.Is(err, ErrSolvedTerminal.Err()) {
		t.Fatal(err)
	}
	if lab.DesiredState != "Stopped" || lab.Revision != 2 {
		t.Fatal(lab)
	}
}
func TestNewPinsMembershipAndPolicy(t *testing.T) {
	in := labInput()
	lab, err := New(in, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if lab.ID == uuid.Nil || lab.OperationID == uuid.Nil || lab.Revision != 1 || lab.ObjectiveCount != 1 || lab.Materialized || lab.SnapshotMode != "skip" || !lab.CreatedAt.Equal(testNow) {
		t.Fatal(lab)
	}
	for _, mutate := range []func(*NewInput){func(v *NewInput) { v.ObjectiveIDs = nil }, func(v *NewInput) { v.ObjectiveIDs = append(v.ObjectiveIDs, v.ObjectiveIDs[0]) }, func(v *NewInput) { v.ObjectiveIDs[0] = uuid.Nil }, func(v *NewInput) { v.EventID = uuid.Nil }, func(v *NewInput) { v.Ref.Lab = "bad/name" }, func(v *NewInput) { v.Generation = -1 }, func(v *NewInput) { v.Policy.SnapshotMode = "invalid" }} {
		invalid := labInput()
		mutate(&invalid)
		if _, err := New(invalid, testNow); err == nil {
			t.Fatal("accepted invalid input", invalid)
		}
	}
}
func TestCloseIdempotentAndManualRestartRequiresRetainedState(t *testing.T) {
	lab, _ := New(labInput(), testNow)
	lab.AgentUID = "uid"
	lab.AgentGeneration = 1
	op := uuid.Must(uuid.NewV7())
	if err := lab.Close("manual", op, testNow); err != nil {
		t.Fatal(err)
	}
	if err := lab.Close("manual", uuid.Must(uuid.NewV7()), testNow.Add(time.Minute)); err != nil || lab.OperationID != op || lab.Revision != 2 {
		t.Fatal(lab, err)
	}
	if err := lab.Start(uuid.Must(uuid.NewV7()), testNow.Add(time.Minute)); err == nil {
		t.Fatal("restart without retained stopped runtime accepted")
	}
	observeStoppedForRestart(t, &lab, testNow.Add(time.Minute), "Retained")
	if err := lab.Start(op, testNow.Add(time.Minute)); err == nil {
		t.Fatal("same operation accepted")
	}
	if err := lab.Start(uuid.Must(uuid.NewV7()), testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if lab.Revision != 3 || lab.ClosedAt != nil || lab.DesiredState != "Running" {
		t.Fatal(lab)
	}
}
func TestObservationNeverCreditsStaleIdentity(t *testing.T) {
	lab, _ := New(labInput(), testNow)
	lab.AgentUID = "uid"
	lab.AgentGeneration = 5
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(time.Minute)
	obs := Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 5, ObservedGeneration: 5, DesiredState: "Stopped", ActualState: "Stopped", AccessFenced: true, ObservedAt: &at, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &at}}
	for _, mutate := range []func(*Observation){func(o *Observation) { o.UID = "old" }, func(o *Observation) { o.Revision-- }, func(o *Observation) { o.OperationID = uuid.Must(uuid.NewV7()) }, func(o *Observation) { o.ObservedGeneration-- }, func(o *Observation) { o.Ref.Lab = "other" }, func(o *Observation) { o.DesiredState = "Running" }} {
		stale := obs
		mutate(&stale)
		if lab.Observe(stale, testNow) {
			t.Fatal("accepted stale observation", stale)
		}
		if lab.Allocation.ReleasedAt != nil || lab.DesiredState != "Stopped" {
			t.Fatal(lab)
		}
	}
	if !lab.Observe(obs, at) || lab.ObservedRevision != lab.Revision || lab.Allocation.ReleasedAt == nil {
		t.Fatal(lab)
	}
	if lab.Observe(obs, at.Add(time.Minute)) {
		t.Fatal("duplicate observation changed aggregate")
	}
}

func TestMatchingFailureCannotCreditRelease(t *testing.T) {
	lab, _ := New(labInput(), testNow)
	lab.AgentUID = "uid"
	lab.AgentGeneration = 1
	lab.Allocation = Allocation{RuntimeState: "Allocated", AllocatedRequests: Compute{CPUMillicores: 50, MemoryBytes: 1024}}
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(time.Minute)
	obs := Observation{Ref: lab.Ref, UID: lab.AgentUID, OperationID: lab.OperationID, Revision: lab.Revision, Generation: 1, ObservedGeneration: 1, DesiredState: "Stopped", ActualState: "StopFailed", FailureCode: "capture_failed", ObservedAt: &at, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &at}}
	if !lab.Observe(obs, at) {
		t.Fatal("matching failure observation was lost")
	}
	if lab.Allocation.RuntimeState != "Allocated" || lab.Allocation.AllocatedRequests.CPUMillicores != 50 || lab.Allocation.ReleasedAt != nil {
		t.Fatal("failure credited release", lab)
	}
}

func TestLiveGenerationAdvancesOnlyOnMatchingStatus(t *testing.T) {
	lab, _ := New(labInput(), testNow)
	lab.AgentUID = "uid"
	lab.AgentGeneration = 1
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(time.Minute)
	o := Observation{Ref: lab.Ref, UID: lab.AgentUID, OperationID: lab.OperationID, Revision: lab.Revision, Generation: 2, ObservedGeneration: 1, DesiredState: "Stopped", ActualState: "Stopped", AccessFenced: true, ObservedAt: &at, Allocation: Allocation{RuntimeState: "Released", ReleasedAt: &at}}
	if lab.Observe(o, at) {
		t.Fatal("old status on newer live metadata accepted")
	}
	o.ObservedGeneration = 2
	if !lab.Observe(o, at) || lab.AgentGeneration != 2 {
		t.Fatal("authoritative live generation could not advance", lab)
	}
	newer := at.Add(time.Minute)
	o.ObservedAt = &newer
	o.Generation = 1
	o.ObservedGeneration = 1
	if lab.Observe(o, newer) {
		t.Fatal("old live generation lowered identity floor")
	}
}

func TestStartClearsPreviousReleaseCertainty(t *testing.T) {
	lab, _ := New(labInput(), testNow)
	lab.AgentUID = "uid"
	lab.AgentGeneration = 1
	at := testNow.Add(time.Minute)
	if err := lab.Close("manual", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	observeStoppedForRestart(t, &lab, at, "Retained")
	if err := lab.Start(uuid.Must(uuid.NewV7()), at); err != nil {
		t.Fatal(err)
	}
	if lab.Allocation.RuntimeState != "Unknown" || lab.Allocation.ReleasedAt != nil {
		t.Fatal("previous stop kept free-capacity certainty on pending restart", lab)
	}
}

func TestUnknownObservationPreservesKnownHeldComputeAndStorage(t *testing.T) {
	lab, _ := New(labInput(), testNow)
	lab.AgentUID = "uid"
	lab.AgentGeneration = 1
	lab.Allocation = Allocation{RuntimeState: "Allocated", StorageState: "Retained", AllocatedRequests: Compute{CPUMillicores: 750, MemoryBytes: 512 * 1024 * 1024}, SnapshotQuotaBytes: 1024 * 1024 * 1024, PhysicalStorageBytes: 123, PhysicalStorageBytesAvailable: true}
	at := testNow.Add(time.Minute)
	o := Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 1, ObservedGeneration: 1, DesiredState: "Running", ActualState: "Unknown", ObservedAt: &at, Allocation: Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}}
	if !lab.Observe(o, at) {
		t.Fatal("unknown metadata was lost")
	}
	if lab.Allocation.AllocatedRequests.CPUMillicores != 750 || lab.Allocation.AllocatedRequests.MemoryBytes != 512*1024*1024 || lab.Allocation.SnapshotQuotaBytes != 1024*1024*1024 || lab.Allocation.PhysicalStorageBytes != 123 {
		t.Fatal("unknown observation freed known resources", lab)
	}
}
func TestRequiredReleaseNeedsSuccessfulCaptureAndAccessFence(t *testing.T) {
	original, _ := New(labInput(), testNow)
	original.AgentUID = "uid"
	original.AgentGeneration = 1
	original.SnapshotMode = "required"
	original.Allocation = Allocation{RuntimeState: "Allocated", StorageState: "Retained", AllocatedRequests: Compute{CPUMillicores: 750}, SnapshotQuotaBytes: 1024 * 1024 * 1024}
	if err := original.Close("solved", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(time.Minute)
	valid := Observation{Ref: original.Ref, UID: "uid", OperationID: original.OperationID, Revision: original.Revision, Generation: 2, ObservedGeneration: 2, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "Succeeded", ObservedAt: &at, AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", StorageState: "Unknown", ReleasedAt: &at}}
	for _, state := range []string{"Unknown", "Failed", "Pending", "Succeeded"} {
		lab := original
		o := valid
		o.SnapshotState = state
		if state == "Succeeded" {
			o.AccessFenced = false
		}
		if !lab.Observe(o, at) {
			t.Fatal("matching metadata lost")
		}
		if lab.Allocation.RuntimeState != "Allocated" || lab.Allocation.AllocatedRequests.CPUMillicores != 750 {
			t.Fatal("uncertified release credited", state, lab)
		}
	}
	lab := original
	if !lab.Observe(valid, at) || lab.Allocation.RuntimeState != "Released" || lab.Allocation.SnapshotQuotaBytes != 1024*1024*1024 {
		t.Fatal("valid release lost retention", lab)
	}
}

func TestSkipLabCanRestartWithoutSnapshotBlob(t *testing.T) {
	lab := certifiedStoppedLab(t, "manual")
	if err := lab.Start(uuid.Must(uuid.NewV7()), testNow.Add(2*time.Minute)); err != nil {
		t.Fatal("preserved stateless definition cannot restart", err)
	}
	if lab.DesiredState != "Running" || lab.ClosedAt != nil {
		t.Fatal(lab)
	}
}
func TestRequiredRestartNeedsCurrentSuccessfulBarrierEvenWithoutNewBlob(t *testing.T) {
	original, _ := New(labInput(), testNow)
	original.SnapshotMode = "required"
	original.AgentUID = "uid"
	original.AgentGeneration = 1
	if err := original.Close("manual", uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(time.Minute)
	observeStoppedForRestart(t, &original, at, "None")
	for _, state := range []string{"Unknown", "Failed", "Pending"} {
		lab := original
		lab.SnapshotState = state
		if err := lab.Start(uuid.Must(uuid.NewV7()), testNow.Add(time.Minute)); err == nil {
			t.Fatal("uncaptured required state restarted", state)
		}
	}
	original.SnapshotState = "Succeeded"
	if err := original.Start(uuid.Must(uuid.NewV7()), at); err != nil {
		t.Fatal("successful unchanged base with no blob cannot restart", err)
	}
}

func certifiedStoppedLab(t *testing.T, reason string) Lab {
	t.Helper()
	l, err := New(labInput(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	l.AgentUID = "uid"
	l.AgentGeneration = 1
	if err = l.Close(reason, uuid.Must(uuid.NewV7()), testNow); err != nil {
		t.Fatal(err)
	}
	observeStoppedForRestart(t, &l, testNow.Add(time.Minute), "None")
	return l
}
func observeStoppedForRestart(t *testing.T, l *Lab, at time.Time, storage string) {
	t.Helper()
	snapshot := "NotRequired"
	if l.SnapshotMode == "required" {
		snapshot = "Succeeded"
	}
	o := Observation{Ref: l.Ref, UID: l.AgentUID, OperationID: l.OperationID, Revision: l.Revision, Generation: l.AgentGeneration + 1, ObservedGeneration: l.AgentGeneration + 1, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: snapshot, ObservedAt: &at, AccessFenced: true, Allocation: Allocation{RuntimeState: "Released", StorageState: storage, ReleasedAt: &at}}
	if !l.Observe(o, at) {
		t.Fatal("invalid certified-stop fixture", l, o)
	}
}
func TestRestartRejectsEventAndUnsupportedClosuresWithoutMutation(t *testing.T) {
	for _, reason := range []string{"event", "", "legacy"} {
		t.Run(reason, func(t *testing.T) {
			l := certifiedStoppedLab(t, "event")
			l.CloseReason = reason
			before := l
			if err := l.Start(uuid.Must(uuid.NewV7()), testNow.Add(2*time.Minute)); err == nil {
				t.Fatal("unsupported closure reopened", reason, l)
			}
			if !reflect.DeepEqual(l, before) {
				t.Fatal("refused restart mutated aggregate", before, l)
			}
		})
	}
}
func TestSkipRestartCannotConsumePreviousStop(t *testing.T) {
	l := certifiedStoppedLab(t, "manual")
	if err := l.Start(uuid.Must(uuid.NewV7()), testNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := l.Close("manual", uuid.Must(uuid.NewV7()), testNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	before := l
	if err := l.Start(uuid.Must(uuid.NewV7()), testNow.Add(4*time.Minute)); err == nil {
		t.Fatalf("restart consumed old stop: revision=%d observed=%d", l.Revision, l.ObservedRevision)
	}
	if !reflect.DeepEqual(l, before) {
		t.Fatal("refused stale restart mutated aggregate")
	}
	observeStoppedForRestart(t, &l, testNow.Add(5*time.Minute), "None")
	if err := l.Start(uuid.Must(uuid.NewV7()), testNow.Add(6*time.Minute)); err != nil {
		t.Fatal("fresh current stop could not restart", err)
	}
}
func TestRestartNeedsCertifiedCurrentStopForBothModes(t *testing.T) {
	for _, mode := range []string{"skip", "required"} {
		for name, mutate := range map[string]func(*Lab){
			"missing_uid": func(l *Lab) { l.AgentUID = "" }, "missing_generation": func(l *Lab) { l.AgentGeneration = 0 },
			"old_revision": func(l *Lab) { l.ObservedRevision-- }, "missing_observation": func(l *Lab) { l.ObservedAt = nil },
			"missing_release_time": func(l *Lab) { l.Allocation.ReleasedAt = nil }, "not_released": func(l *Lab) { l.Allocation.RuntimeState = "Allocated" },
			"unfenced": func(l *Lab) { l.AccessFenced = false }, "failed": func(l *Lab) { l.FailureCode = "stop_failed" },
			"stop_failed": func(l *Lab) { l.ActualState = "StopFailed" }, "no_logical_close": func(l *Lab) { l.ClosedAt = nil },
		} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				l := certifiedStoppedLab(t, "manual")
				l.SnapshotMode = mode
				l.SnapshotState = "Succeeded"
				mutate(&l)
				before := l
				if err := l.Start(uuid.Must(uuid.NewV7()), testNow.Add(2*time.Minute)); err == nil {
					t.Fatal("uncertified stop restarted", name, l)
				}
				if !reflect.DeepEqual(l, before) {
					t.Fatal("refused restart mutated aggregate")
				}
			})
		}
	}
}
func TestCurrentManualAndStageStopsCanRestart(t *testing.T) {
	for _, reason := range []string{"manual", "stage"} {
		t.Run(reason, func(t *testing.T) {
			l := certifiedStoppedLab(t, reason)
			if err := l.Start(uuid.Must(uuid.NewV7()), testNow.Add(2*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if l.DesiredState != "Running" || l.Revision != 3 || l.ClosedAt != nil {
				t.Fatal(l)
			}
		})
	}
}
