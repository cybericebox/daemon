package exerciseModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestTestDeploy_StaticIsTheRowShape(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	id, ver, variant, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	s := exerciseModel.NewStaticTestDeploy(id, ver, variant, owner, nil, now, 2*time.Hour)
	if !s.IsStatic() || s.Kind() != exerciseModel.DeployKindStatic || !s.ExpiresAt.Equal(now.Add(2*time.Hour)) || !s.CreatedAt.Equal(now) {
		t.Fatalf("%+v", s)
	}
	if (exerciseModel.TestDeploy{GroupName: "tu-x", LabName: "l-1"}).IsStatic() {
		t.Fatal("lab row")
	}
	ev := uuid.Must(uuid.NewV7())
	if (exerciseModel.TestDeploy{EventID: &ev}).IsStatic() {
		t.Fatal("event preview rows are never static")
	}
	if (exerciseModel.TestDeploy{GroupName: "tu-x", LabName: "l-1"}).Kind() != exerciseModel.DeployKindLab {
		t.Fatal("lab kind")
	}
}

// The lease of a test lab counts only while the lab runs: a lab the cluster has not started yet (queued, waiting for a
// node, withdrawn) gets a fresh lease from now; a running lab's lease counts from when it started; the hard limit holds.
func TestTestLeaseEnd(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	lease := 30 * time.Minute
	limit := now.Add(4 * time.Hour)
	started := func(at time.Time) []exerciseModel.LabDeployedDevice {
		return []exerciseModel.LabDeployedDevice{
			{Name: "a", Ready: true, Scheduling: &exerciseModel.PodScheduling{State: exerciseModel.PodStateStarted, StartedAt: at.Add(-time.Minute)}},
			{Name: "b", Ready: true, Scheduling: &exerciseModel.PodScheduling{State: exerciseModel.PodStateStarted, StartedAt: at}},
		}
	}
	cases := []struct {
		name   string
		status exerciseModel.LabDeployStatus
		limit  time.Time
		due    time.Time
		extend bool
	}{
		{"queued", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued, Queue: &exerciseModel.LabQueue{Position: 2, Reason: exerciseModel.QueueReasonInsufficientResource}}, limit, now.Add(lease), true},
		{"withdrawn", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued, Queue: &exerciseModel.LabQueue{Position: 1, Reason: exerciseModel.QueueReasonWithdrawn, Attempt: 2}}, limit, now.Add(lease), true},
		{"node starting", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning, Queue: &exerciseModel.LabQueue{Reason: exerciseModel.QueueReasonNodeStarting}}, limit, now.Add(lease), true},
		{"pods not all sent", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning, Queue: &exerciseModel.LabQueue{Pods: 3, Pending: 1}}, limit, now.Add(lease), true},
		{"running since late", exerciseModel.LabDeployStatus{Ready: true, Phase: exerciseModel.DeployPhaseReady, Devices: started(now.Add(-10 * time.Minute))}, limit, now.Add(20 * time.Minute), true},
		{"running the whole lease", exerciseModel.LabDeployStatus{Ready: true, Phase: exerciseModel.DeployPhaseReady, Devices: started(now.Add(-lease))}, limit, time.Time{}, false},
		{"broken, not the cluster's wait", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning, Devices: []exerciseModel.LabDeployedDevice{{Name: "a", Reason: "ImagePullBackOff"}}}, limit, time.Time{}, false},
		{"failed", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseFailed}, limit, time.Time{}, false},
		{"capped by the limit", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued}, now.Add(10 * time.Minute), now.Add(10 * time.Minute), true},
		{"limit reached", exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued}, now, time.Time{}, false},
	}
	for _, c := range cases {
		due, extend := exerciseModel.TestLeaseEnd(c.status, lease, c.limit, now)
		if extend != c.extend || !due.Equal(c.due) {
			t.Errorf("%s: due %v extend %v, want %v %v", c.name, due, extend, c.due, c.extend)
		}
	}
}
