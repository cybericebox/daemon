package eventStandModel

import (
	"errors"
	"strings"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

var standNow = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func TestNewTimingValidatesBounds(t *testing.T) {
	if _, err := NewTiming(0); err != nil {
		t.Fatalf("lower bound rejected: %v", err)
	}
	if _, err := NewTiming(10080); err != nil {
		t.Fatalf("upper bound rejected: %v", err)
	}
	for _, delay := range []int32{-1, 10081} {
		if _, err := NewTiming(delay); !errors.Is(err, ErrStandSettingsInvalid.Err()) {
			t.Fatalf("NewTiming(%d) = %v, want settings invalid", delay, err)
		}
	}
}

func TestTimingTeardown(t *testing.T) {
	timing := Timing{TeardownDelayMinutes: 60}
	if timing.TeardownAt(nil) != nil {
		t.Fatal("an event without finish must never be torn down by the schedule")
	}
	finish := standNow.Add(3 * time.Hour)
	if got := timing.TeardownAt(&finish); got == nil || !got.Equal(finish.Add(time.Hour)) {
		t.Fatalf("TeardownAt = %v", got)
	}
}

func TestAssessPrefersFailureThenPreparation(t *testing.T) {
	cases := []struct {
		in     LabCounters
		status Status
	}{
		{LabCounters{}, StatusReady},
		{LabCounters{PendingLabs: 1}, StatusCreating},
		{LabCounters{MissingAssignments: 2}, StatusCreating},
		{LabCounters{PendingLabs: 3, FailedLabs: 1, FailureReason: "boom"}, StatusFailed},
	}
	for _, c := range cases {
		status, reason := Assess(c.in)
		if status != c.status {
			t.Fatalf("Assess(%+v) = %v, want %v", c.in, status, c.status)
		}
		if status == StatusFailed && reason != "boom" {
			t.Fatalf("failure reason = %q", reason)
		}
	}
}

func TestClassify(t *testing.T) {
	deployed := standNow.Add(-time.Minute)
	if outcome, _ := Classify(exerciseModel.LabDeployStatus{Ready: true}, deployed, standNow); outcome != OutcomeReady {
		t.Fatalf("ready lab = %v", outcome)
	}
	if outcome, reason := Classify(exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseFailed}, deployed, standNow); outcome != OutcomeFailed || reason == "" {
		t.Fatalf("failed phase = %v %q", outcome, reason)
	}
	bad := exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning, Devices: []exerciseModel.LabDeployedDevice{{Name: "web", Reason: "ImagePullBackOff"}}}
	if outcome, reason := Classify(bad, deployed, standNow); outcome != OutcomeFailed || reason != "ImagePullBackOff: web" {
		t.Fatalf("bad image = %v %q", outcome, reason)
	}
	waiting := exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning, Devices: []exerciseModel.LabDeployedDevice{{Name: "db"}, {Name: "web", Ready: true}}}
	if outcome, _ := Classify(waiting, deployed, standNow); outcome != OutcomeWaiting {
		t.Fatalf("provisioning lab = %v", outcome)
	}
	outcome, reason := Classify(waiting, standNow.Add(-DeployTimeout), standNow)
	if outcome != OutcomeFailed || !strings.Contains(reason, "devices not ready: db") {
		t.Fatalf("timed out lab = %v %q", outcome, reason)
	}
}

func TestReasonIsTrimmedAndBounded(t *testing.T) {
	if got := Reason("  a \n b  "); got != "a b" {
		t.Fatalf("Reason = %q", got)
	}
	if got := []rune(Reason(strings.Repeat("я", 400))); len(got) != reasonMaxLen {
		t.Fatalf("Reason length = %d", len(got))
	}
}

func TestInfrastructureOpenIsStrictAndSticky(t *testing.T) {
	if open, _ := InfrastructureOpen(false, nil, true); open {
		t.Fatal("infrastructure challenges must not open before the start")
	}
	if open, _ := InfrastructureOpen(true, nil, false); open {
		t.Fatal("infrastructure challenges must wait for every stand")
	}
	if open, openNow := InfrastructureOpen(true, nil, true); !open || !openNow {
		t.Fatal("all stands ready after start must open and record the barrier")
	}
	opened := standNow
	if open, openNow := InfrastructureOpen(true, &opened, false); !open || openNow {
		t.Fatal("an opened barrier must stay open without re-recording")
	}
}

func TestStatusStrings(t *testing.T) {
	want := map[Status]string{StatusNotDeployed: "not_deployed", StatusCreating: "creating", StatusReady: "ready", StatusFailed: "failed", StatusRemoved: "removed"}
	for status, text := range want {
		if status.String() != text {
			t.Fatalf("%d.String() = %q", status, status.String())
		}
	}
}

func TestClassifyQueuedLabNeverTimesOutWhileWaiting(t *testing.T) {
	deployedAt := standNow.Add(-2 * DeployTimeout)
	queued := exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued, Queue: &exerciseModel.LabQueue{Position: 4, Length: 10, Reason: exerciseModel.QueueReasonInFlightLimit, Pods: 2, Pending: 2}}
	if outcome, _ := Classify(queued, deployedAt, standNow); outcome != OutcomeWaiting {
		t.Fatalf("queued lab outcome = %v, want waiting", outcome)
	}
	partial := exerciseModel.LabDeployStatus{Phase: "Provisioning", Queue: &exerciseModel.LabQueue{Pods: 3, Pending: 1, Reason: exerciseModel.QueueReasonWaitingForTurn}}
	if outcome, _ := Classify(partial, deployedAt, standNow); outcome != OutcomeWaiting {
		t.Fatalf("lab with undispatched pods outcome = %v, want waiting", outcome)
	}
}

func TestClassifyTimeoutCountsFromTheLastDispatch(t *testing.T) {
	deployedAt := standNow.Add(-2 * DeployTimeout)
	dispatched := standNow.Add(-DeployTimeout / 2)
	status := exerciseModel.LabDeployStatus{Phase: "Provisioning", Devices: []exerciseModel.LabDeployedDevice{
		{Name: "web", Scheduling: &exerciseModel.PodScheduling{State: exerciseModel.PodStateStarting, DispatchedAt: dispatched}},
	}}
	if outcome, _ := Classify(status, deployedAt, standNow); outcome != OutcomeWaiting {
		t.Fatalf("recently dispatched lab outcome = %v, want waiting", outcome)
	}
	if outcome, _ := Classify(status, deployedAt, dispatched.Add(DeployTimeout)); outcome != OutcomeFailed {
		t.Fatalf("lab stuck after dispatch outcome = %v, want failed", outcome)
	}
}

func TestClassifyFailsOnASchedulerFailureButWaitsOutAStartupTimeout(t *testing.T) {
	pull := exerciseModel.LabDeployStatus{Phase: "Provisioning", Devices: []exerciseModel.LabDeployedDevice{
		{Name: "web", Scheduling: &exerciseModel.PodScheduling{State: exerciseModel.PodStateFailed, Failure: &exerciseModel.PodFailure{Reason: "ImagePull", Message: "not found"}}},
	}}
	outcome, reason := Classify(pull, standNow, standNow)
	if outcome != OutcomeFailed || !strings.Contains(reason, "ImagePull: web (not found)") {
		t.Fatalf("outcome = %v, reason %q", outcome, reason)
	}
	slow := pull
	slow.Devices = []exerciseModel.LabDeployedDevice{{Name: "web", Scheduling: &exerciseModel.PodScheduling{State: exerciseModel.PodStateFailed, Failure: &exerciseModel.PodFailure{Reason: "StartupTimeout"}}}}
	if outcome, _ = Classify(slow, standNow, standNow); outcome != OutcomeWaiting {
		t.Fatalf("a startup timeout may still become ready, outcome = %v", outcome)
	}
}
