package lab

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// eventGroup is the lab group of an event team; the test deploys use other names (tu-<user>).
var eventGroup = func() string {
	name, err := labBindingModel.GroupName(uuid.FromStringOrNil("01a12a96-cb5c-748f-aebd-ab97d42cf174"), uuid.FromStringOrNil("01a1466c-bfed-7eff-97c3-07e549a387c8"))
	if err != nil {
		panic(err)
	}
	return name
}()

func throttledLab(uid string, devices map[string]int32) *labpb.Lab {
	lab := &labpb.Lab{Namespace: "ns", Name: "lab-" + uid, Uid: uid, LabGroupName: eventGroup, Status: &labpb.LabStatus{}}
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
	assert.Equal(t, []ThrottleAlert{{AgentID: "agent-1", Namespace: "ns", LabName: "lab-u1", LabGroupName: eventGroup, Run: "u1", Device: "db", Percent: 60, WindowSeconds: 300}}, a.got, "50 is not above 50; 0 is no data")

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

func TestThrottleWatcherIgnoresLabsThatAreNotAnEventTeams(t *testing.T) {
	a := &alerts{}
	w := NewThrottleWatcher("agent-1", a.notify)
	ctx := context.Background()
	hot := map[string]int32{"db": 90}

	// A catalog test deploy or an event preview runs in a tu-<user> group, with a small CPU on purpose.
	test := throttledLab("u1", hot)
	test.LabGroupName = "tu-01a12a96-cb5c-748f-aebd-ab97d42cf174"
	empty := throttledLab("u2", hot)
	empty.LabGroupName = ""
	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{test, empty}})
	assert.Empty(t, a.got, "no alert for a lab outside an event team")

	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{test, throttledLab("u3", hot)}})
	assert.Len(t, a.got, 1, "an event team's lab is alerted")
	assert.Equal(t, "u3", a.got[0].Run)
}

func TestThrottleWatcherClosesTheAlertsOfGoneLabRuns(t *testing.T) {
	a := &alerts{}
	var closed []string
	w := NewThrottleWatcher("agent-1", a.notify).WithGone(func(_ context.Context, run, device string) error {
		closed = append(closed, run+"/"+device)
		return nil
	})
	ctx := context.Background()

	w.Observe(ctx, &labpb.MonitoringUpdate{Labs: []*labpb.Lab{throttledLab("u1", map[string]int32{"db": 80}), throttledLab("u2", map[string]int32{"web": 90})}})
	w.Observe(ctx, &labpb.MonitoringUpdate{DeletedKeys: []*labpb.MonitoringDeletedKey{{Kind: "Lab", Namespace: "ns", Name: "lab-u1"}}})
	assert.Equal(t, []string{"u1/db"}, closed, "a deleted lab closes its device alerts")

	w.Observe(ctx, &labpb.MonitoringUpdate{DeletedKeys: []*labpb.MonitoringDeletedKey{{Kind: "Lab", Namespace: "ns", Name: "lab-unknown"}}})
	assert.Len(t, closed, 1, "an unknown lab closes nothing")

	w.Observe(ctx, &labpb.MonitoringUpdate{Snapshot: true, Labs: []*labpb.Lab{throttledLab("u3", map[string]int32{"idle": 0})}})
	assert.Equal(t, []string{"u1/db", "u2/web"}, closed, "a lab missing from a snapshot closes its alerts")

	w.Observe(ctx, &labpb.MonitoringUpdate{Snapshot: true})
	assert.Len(t, closed, 2, "a lab without alerts closes nothing")
}
