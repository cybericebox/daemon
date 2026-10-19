package lab

import (
	"testing"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

func withdrawn(n int32, retry int64) *labpb.PodScheduling {
	return &labpb.PodScheduling{State: labpb.PodState_POD_STATE_QUEUED, Withdrawals: n, RetryAfterUnixMs: retry}
}

// A group reaches the alert at its third withdrawal in a row, once per message whatever number of its labs come in it;
// the series is the retry time of that withdrawal. Other counts, and a group sent again (no retry time), say nothing.
func TestRepeatedWithdrawals(t *testing.T) {
	lab := func(name, group string, pods ...*labpb.PodScheduling) *labpb.Lab {
		st := &labpb.LabStatus{Phase: "Queued", Scheduling: &labpb.Scheduling{Group: group, Reason: "Withdrawn"}}
		for _, p := range pods {
			st.Devices = append(st.Devices, &labpb.LabDeviceStatus{Name: "d", Scheduling: p})
		}
		return &labpb.Lab{Name: name, LabGroupName: "g-" + name, Status: st}
	}
	update := &labpb.MonitoringUpdate{
		Labs: []*labpb.Lab{
			lab("a", "task-1", withdrawn(3, 1000), withdrawn(3, 1000)),
			lab("b", "task-1", withdrawn(3, 1000)),
			lab("c", "", withdrawn(2, 2000)),
			lab("d", "", withdrawn(3, 0)),
			lab("e", "", withdrawn(4, 3000)),
		},
		Groups: []*labpb.LabGroup{{Name: "team-1", Status: &labpb.LabGroupStatus{Pods: []*labpb.LabGroupPod{{Name: "vpn", Scheduling: withdrawn(3, 4000)}}}}},
	}
	got := repeatedWithdrawals(update)
	want := []labMonitoringModel.Withdrawal{
		{Group: "deploy group task-1", Attempt: 3, Series: 1000},
		{Group: "lab group team-1", Attempt: 3, Series: 4000},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}
