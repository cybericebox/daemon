package lab

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

func throttledLab(uid string, devices map[string]int32) *labpb.Lab {
	lab := &labpb.Lab{Namespace: "ns", Name: "lab-" + uid, Uid: uid, LabGroupName: "group", Status: &labpb.LabStatus{}}
	for name, percent := range devices {
		lab.Status.Devices = append(lab.Status.Devices, &labpb.LabDeviceStatus{Name: name, CpuThrottledPercent: percent, CpuThrottleWindowSeconds: 300})
	}
	return lab
}

type alerts struct {
	got []ThrottleAlert
	err error
}

func (a *alerts) notify(_ context.Context, alert ThrottleAlert) error {
	if a.err != nil {
		return a.err
	}
	a.got = append(a.got, alert)
	return nil
}

func TestThrottleWatcherAlertsAboveFiftyPercentOncePerDevicePerLabRun(t *testing.T) {
	a := &alerts{}
	w := NewThrottleWatcher("agent-1", a.notify)
	ctx := context.Background()

	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", map[string]int32{"web": 50, "db": 60, "idle": 0})}})
	assert.Equal(t, []ThrottleAlert{{AgentID: "agent-1", Namespace: "ns", LabName: "lab-u1", LabGroupName: "group", Run: "u1", Device: "db", Percent: 60, WindowSeconds: 300}}, a.got, "50 is not above 50; 0 is no data")

	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", map[string]int32{"db": 90, "web": 70})}})
	assert.Len(t, a.got, 2, "db is not alerted again, web is new")
	assert.Equal(t, "web", a.got[1].Device)

	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u2", map[string]int32{"db": 80})}})
	assert.Len(t, a.got, 3, "another lab run alerts again")
}

func TestThrottleWatcherForgetsDeletedLabsAndLabsMissingFromASnapshot(t *testing.T) {
	a := &alerts{}
	w := NewThrottleWatcher("agent-1", a.notify)
	ctx := context.Background()
	hot := map[string]int32{"db": 80}

	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", hot)}})
	w.Observe(ctx, &labpb.MonitoringUpdate{DeletedKeys: []*labpb.MonitoringDeletedKey{{Kind: "Lab", Namespace: "ns", Name: "lab-u1"}}})
	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", hot)}})
	assert.Len(t, a.got, 2, "a deleted lab that comes back is a new run")

	w.Observe(ctx, &labpb.MonitoringUpdate{Snapshot: true})
	assert.Empty(t, w.alerted, "a snapshot without the lab drops what was remembered")
}

func TestThrottleWatcherKeepsRetryingAFailedAlert(t *testing.T) {
	a := &alerts{err: errors.New("inbox down")}
	w := NewThrottleWatcher("agent-1", a.notify)
	ctx := context.Background()
	update := &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", map[string]int32{"db": 80})}}

	w.Observe(ctx, update)
	a.err = nil
	w.Observe(ctx, update)
	w.Observe(ctx, update)
	assert.Len(t, a.got, 1, "delivered once after the failure, then remembered")
}

func TestThrottleWatcherToleratesLabsWithoutStatusAndNilNotifier(t *testing.T) {
	a := &alerts{}
	w := NewThrottleWatcher("agent-1", a.notify)
	w.Observe(context.Background(), &labpb.MonitoringUpdate{Labs: []*labpb.Lab{{Name: "bare"}}})
	assert.Empty(t, a.got)
	NewThrottleWatcher("agent-1", nil).Observe(context.Background(), &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", map[string]int32{"db": 80})}})
}
