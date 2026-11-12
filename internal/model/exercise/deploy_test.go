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

func TestLabDeployedDeviceLimitNotice(t *testing.T) {
	if got := (LabDeployedDevice{CPUThrottledPercent: 50}).LimitNotice(); got != "" {
		t.Fatalf("50%% is not above the threshold: %q", got)
	}
	if got := (LabDeployedDevice{CPUThrottledPercent: 51}).LimitNotice(); got != DeviceLimitCPU {
		t.Fatalf("51%% = %q, want %q", got, DeviceLimitCPU)
	}
}

// One lookup for every access kind: http by its port, the other kinds by their protocol; an entry with no url yet is not an address.
func TestLabDeployStatusEntryURL(t *testing.T) {
	status := LabDeployStatus{Access: []LabAccess{
		{Device: "web", Port: 80, Protocol: "http", URL: "https://web-a1w.labs.test"},
		{Device: "web", Port: 8443, Protocol: "https", URL: "https://web-a1-8443w.labs.test"},
		{Device: "web", Protocol: "terminal", URL: "https://web-a1t.labs.test"},
		{Device: "db", Protocol: "terminal"},
	}}
	for name, tc := range map[string]struct {
		device string
		kind   AccessKind
		port   int32
		want   string
	}{
		"http port":       {"web", AccessHTTP, 80, "https://web-a1w.labs.test"},
		"https port":      {"web", AccessHTTP, 8443, "https://web-a1-8443w.labs.test"},
		"unknown port":    {"web", AccessHTTP, 81, ""},
		"terminal":        {"web", AccessTerminal, 0, "https://web-a1t.labs.test"},
		"terminal no url": {"db", AccessTerminal, 0, ""},
		"vnc not served":  {"web", AccessVNC, 0, ""},
		"other device":    {"x", AccessTerminal, 0, ""},
	} {
		got, ok := status.EntryURL(tc.device, tc.kind, tc.port)
		if got != tc.want || ok != (tc.want != "") {
			t.Fatalf("%s: %q %v", name, got, ok)
		}
	}
	if got, ok := status.WebURL("web", 80); !ok || got != "https://web-a1w.labs.test" {
		t.Fatalf("WebURL: %q %v", got, ok)
	}
}
