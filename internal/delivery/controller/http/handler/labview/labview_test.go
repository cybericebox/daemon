package labview

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labMonitoring "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

func TestStatusMapsQueueSnapshotAndWarnings(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	out := Status(exerciseModel.LabDeployStatus{
		Phase: exerciseModel.DeployPhaseQueued, ImageWarning: "web:latest", GroupImageWarning: "vpn:1",
		Queue: &exerciseModel.LabQueue{Position: 2, Length: 7, Reason: exerciseModel.QueueReasonPreparingImages, Message: "preparing", Group: "hidden-task", Pods: 3, Pending: 2},
		Devices: []exerciseModel.LabDeployedDevice{
			{Name: "plain"},
			{Name: "slow", Scheduling: &exerciseModel.PodScheduling{State: exerciseModel.PodStateFailed, Failure: &exerciseModel.PodFailure{Reason: "ImagePull", Message: "denied", RestartCount: 1}}},
			{Name: "web", Snapshot: &exerciseModel.DeviceSnapshot{LastSnapshotAt: at, SizeBytes: 10, Warning: "quota", Rescue: true}},
		},
	})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{`"Phase":"Queued"`, `"Queue":{"Position":2,"Length":7,"Reason":"PreparingImages","Message":"preparing","Pods":3,"Pending":2,"Attempt":0,"RetryAt":null,"WaitingSince":null}`, `"ImageWarning":"web:latest"`, `"GroupImageWarning":"vpn:1"`,
		`{"Name":"plain","Ready":false,"Reason":"","Scheduling":null,"Snapshot":null}`, `"Scheduling":{"State":"Failed","QueuedAt":null,"DispatchedAt":null,"StartedAt":null,"Failure":{"Reason":"ImagePull","Message":"denied","RestartCount":1,"At":null}}`, `"LastSnapshotAt":"2026-10-01T09:00:00Z","RestoredAt":null,"SizeBytes":10,"Warning":"quota","Rescue":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("response lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hidden-task") {
		t.Errorf("the deploy group must not reach the client: %s", got)
	}
	if never := Status(exerciseModel.LabDeployStatus{Phase: "Ready"}); never.Queue != nil {
		t.Errorf("queue = %+v", never.Queue)
	}
}

func TestAnnotateDevicesFillsTypeAndLogicalName(t *testing.T) {
	s := StatusResponse{Devices: []DeviceResponse{{Name: "web"}, {Name: "sw-abc"}, {Name: "vpn"}, {Name: "mystery"}}}
	AnnotateDevices(&s, map[string]DeviceInfo{"web": {Name: "web", Type: "container"}, "sw-abc": {Name: "sw", Type: "unmanaged-switch"}})
	got := [][2]string{}
	for _, d := range s.Devices {
		got = append(got, [2]string{d.Type, d.LogicalName})
	}
	want := [][2]string{{"container", "web"}, {"unmanaged-switch", "sw"}, {"vpn", ""}, {"", ""}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("device %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// A withdrawn lab carries its attempt and retry time, a lab waiting for a node since when; a stand counts the labs waiting
// for a node, and gets a queue for them alone.
func TestQueueDetailsOfWithdrawalAndNodeWait(t *testing.T) {
	at := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(Queue(&exerciseModel.LabQueue{Position: 1, Length: 1, Reason: exerciseModel.QueueReasonWithdrawn, Attempt: 2, RetryAt: at}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `"Reason":"Withdrawn","Message":"","Pods":0,"Pending":0,"Attempt":2,"RetryAt":"2026-10-19T10:00:00Z","WaitingSince":null`; !strings.Contains(string(raw), want) {
		t.Fatalf("queue = %s, want %s", raw, want)
	}
	stand := StandQueue(labMonitoring.Launch{NodeStartingLabs: 2, WaitingSince: at})
	if stand == nil {
		t.Fatal("a stand with labs waiting for a node has a queue")
	}
	raw, err = json.Marshal(stand)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"QueuedLabs":0,"Position":0,"Length":0,"Reason":"NodeStarting","NodeStartingLabs":2,"Attempt":0,"RetryAt":null,"WaitingSince":"2026-10-19T10:00:00Z"}`; string(raw) != want {
		t.Fatalf("stand queue = %s, want %s", raw, want)
	}
	queued := StandQueue(labMonitoring.Launch{QueuedLabs: 1, Position: 3, Length: 5, Reason: exerciseModel.QueueReasonWithdrawn, Attempt: 1, RetryAt: at, NodeStartingLabs: 1, WaitingSince: at})
	if queued.Reason != exerciseModel.QueueReasonWithdrawn || queued.Attempt != 1 || queued.RetryAt == nil || queued.NodeStartingLabs != 1 {
		t.Fatalf("stand queue = %+v", queued)
	}
	if StandQueue(labMonitoring.Launch{}) != nil {
		t.Fatal("a stand with nothing waiting has no queue")
	}
}
