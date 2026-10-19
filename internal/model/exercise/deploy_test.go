package exerciseModel

import (
	"testing"
	"time"
)

var queueNow = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

// A withdrawn lab shows the attempt and retry time of its pods (the most of them).
func TestLabQueueDetailsOfAWithdrawal(t *testing.T) {
	status := LabDeployStatus{Queue: &LabQueue{Position: 1, Length: 2, Reason: QueueReasonWithdrawn}, Devices: []LabDeployedDevice{
		{Name: "web", Scheduling: &PodScheduling{State: PodStateQueued, Withdrawals: 2, RetryAfter: queueNow.Add(time.Minute)}},
		{Name: "db", Scheduling: &PodScheduling{State: PodStateQueued, Withdrawals: 3, RetryAfter: queueNow.Add(2 * time.Minute)}},
		{Name: "sw"},
	}}
	status.FillQueueDetails()
	if q := status.Queue; q.Attempt != 3 || !q.RetryAt.Equal(queueNow.Add(2*time.Minute)) || !q.WaitingSince.IsZero() {
		t.Fatalf("queue = %+v", q)
	}
}

// A lab waiting for a node shows since when: the earliest dispatch of a pod still starting.
func TestLabQueueDetailsOfANodeWait(t *testing.T) {
	status := LabDeployStatus{Queue: &LabQueue{Reason: QueueReasonNodeStarting}, Devices: []LabDeployedDevice{
		{Name: "db", Scheduling: &PodScheduling{State: PodStateStarting, DispatchedAt: queueNow.Add(-7 * time.Minute)}},
		{Name: "ui", Scheduling: &PodScheduling{State: PodStateStarting, DispatchedAt: queueNow.Add(-6 * time.Minute)}},
		{Name: "ok", Scheduling: &PodScheduling{State: PodStateStarted, DispatchedAt: queueNow.Add(-10 * time.Minute)}},
	}}
	status.FillQueueDetails()
	if q := status.Queue; !q.WaitingSince.Equal(queueNow.Add(-7*time.Minute)) || q.Attempt != 0 || !q.RetryAt.IsZero() {
		t.Fatalf("queue = %+v", q)
	}
}

// Other reasons, and a lab without a queue, get no details.
func TestLabQueueDetailsOnlyForTheirReasons(t *testing.T) {
	status := LabDeployStatus{Queue: &LabQueue{Position: 1, Reason: QueueReasonInFlightLimit}, Devices: []LabDeployedDevice{
		{Name: "web", Scheduling: &PodScheduling{State: PodStateStarting, DispatchedAt: queueNow, Withdrawals: 1, RetryAfter: queueNow}},
	}}
	status.FillQueueDetails()
	if q := status.Queue; q.Attempt != 0 || !q.RetryAt.IsZero() || !q.WaitingSince.IsZero() {
		t.Fatalf("queue = %+v", q)
	}
	none := LabDeployStatus{}
	none.FillQueueDetails()
	if none.Queue != nil {
		t.Fatalf("queue = %+v", none.Queue)
	}
}
