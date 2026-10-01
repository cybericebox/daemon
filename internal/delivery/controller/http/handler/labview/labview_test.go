package labview

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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
	for _, want := range []string{`"Phase":"Queued"`, `"Queue":{"Position":2,"Length":7,"Reason":"PreparingImages","Message":"preparing","Pods":3,"Pending":2}`, `"ImageWarning":"web:latest"`, `"GroupImageWarning":"vpn:1"`,
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
