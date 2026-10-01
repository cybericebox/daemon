package labagent

import (
	"testing"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestMapLabStatus_QueueSnapshotAndWarnings(t *testing.T) {
	out := mapLabStatus(&labpb.Lab{Status: &labpb.LabStatus{
		Phase:        "Queued",
		ImageWarning: "web:latest",
		Scheduling:   &labpb.Scheduling{Group: "task-1", Position: 3, Length: 9, Reason: "InFlightLimit", Pods: 4, Pending: 4},
		Devices: []*labpb.LabDeviceStatus{
			{Name: "plain"},
			{Name: "web", Snapshot: &labpb.DeviceSnapshotStatus{LastSnapshotUnixMs: 1_700_000_000_000, SizeBytes: 42, Warning: "quota", Rescue: true},
				Scheduling: &labpb.PodScheduling{State: labpb.PodState_POD_STATE_FAILED, DispatchedUnixMs: 1_700_000_100_000,
					Failure: &labpb.PodFailure{Reason: "ImagePull", Message: "not found", RestartCount: 2, AtUnixMs: 1_700_000_200_000}}},
		},
	}})
	if out.Phase != "Queued" || out.ImageWarning != "web:latest" {
		t.Fatalf("phase/warning: %+v", out)
	}
	if out.Queue == nil || out.Queue.Position != 3 || out.Queue.Length != 9 || out.Queue.Reason != "InFlightLimit" || out.Queue.Pods != 4 || out.Queue.Pending != 4 || out.Queue.Group != "task-1" {
		t.Fatalf("queue: %+v", out.Queue)
	}
	if out.Devices[0].Snapshot != nil {
		t.Fatalf("device without persistence must have no snapshot")
	}
	sn := out.Devices[1].Snapshot
	sched := out.Devices[1].Scheduling
	if sched == nil || sched.State != exerciseModel.PodStateFailed || sched.Failure == nil || sched.Failure.Reason != "ImagePull" || sched.Failure.RestartCount != 2 || !sched.DispatchedAt.Equal(time.UnixMilli(1_700_000_100_000)) {
		t.Fatalf("scheduling: %+v", sched)
	}
	if out.Devices[0].Scheduling != nil {
		t.Fatalf("an untracked device has no scheduling")
	}
	if sn == nil || !sn.LastSnapshotAt.Equal(time.UnixMilli(1_700_000_000_000)) || !sn.RestoredAt.IsZero() || sn.SizeBytes != 42 || sn.Warning != "quota" || !sn.Rescue {
		t.Fatalf("snapshot: %+v", sn)
	}
}

func TestMapLabStatus_NoQueueForUnqueuedLab(t *testing.T) {
	if out := mapLabStatus(&labpb.Lab{Status: &labpb.LabStatus{Phase: "Ready", Ready: true}}); out.Queue != nil {
		t.Fatalf("queue = %+v", out.Queue)
	}
}
