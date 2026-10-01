package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type fakeLabAgent struct {
	Agent
	statuses map[string]exerciseModel.LabDeployStatus
}

func (f fakeLabAgent) LabStatus(_ context.Context, group, _ string) (exerciseModel.LabDeployStatus, error) {
	status, ok := f.statuses[group]
	if !ok {
		return exerciseModel.LabDeployStatus{}, errors.New("agent down")
	}
	return status, nil
}

type recordingDestroyer struct {
	owner, deploy uuid.UUID
	err           error
}

func (r *recordingDestroyer) DestroyDeployTest(_ context.Context, userID, deployID uuid.UUID) error {
	r.owner, r.deploy = userID, deployID
	return r.err
}

func newTestLabs(t *testing.T, agent Agent, destroyer TestLabDestroyer) (*TestLabsUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	return NewTestLabsUseCase(TestLabsDependencies{Source: platformStandRepo.New(q), Agent: agent, Destroyer: destroyer, Now: func() time.Time { return fixedNow }}), q
}

func TestListTestLabsMapsAuthorExerciseLeaseAndLiveState(t *testing.T) {
	agent := fakeLabAgent{statuses: map[string]exerciseModel.LabDeployStatus{
		"t-ready":   {Ready: true, Phase: exerciseModel.DeployPhaseReady, UsageAvailable: true, CPUMillicores: 400, MemoryBytes: 4096},
		"t-failed":  {Phase: exerciseModel.DeployPhaseFailed},
		"t-pending": {Phase: exerciseModel.DeployPhaseProvisioning},
	}}
	uc, q := newTestLabs(t, agent, nil)
	author, exercise := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row := func(group string, expires time.Time) postgres.ListPlatformTestLabsRow {
		return postgres.ListPlatformTestLabsRow{
			ID: uuid.Must(uuid.NewV7()), GroupName: group, CreatedBy: author, ExerciseID: exercise, ExerciseName: "Web 1", VariantNumber: 2,
			AuthorFirstName: "Ann", AuthorLastName: "Lee", AuthorEmail: "ann@example.test", CreatedAt: fixedNow.Add(-time.Hour), ExpiresAt: expires, Total: 5,
		}
	}
	q.EXPECT().ListPlatformTestLabs(gomock.Any(), postgres.ListPlatformTestLabsParams{Search: "web", LimitVal: 2, OffsetVal: 2}).Return([]postgres.ListPlatformTestLabsRow{
		row("t-ready", fixedNow.Add(time.Hour)), row("t-failed", fixedNow.Add(-time.Minute)), row("t-pending", fixedNow.Add(time.Hour)), row("t-gone", fixedNow.Add(time.Hour)),
	}, nil)

	page, err := uc.ListTestLabs(context.Background(), "web", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || page.Page != 2 || len(page.Items) != 4 {
		t.Fatalf("page = %+v", page)
	}
	first := page.Items[0]
	if first.AuthorName != "Ann Lee" || first.AuthorEmail != "ann@example.test" || first.AuthorID != author || first.ExerciseID != exercise || first.ExerciseName != "Web 1" || first.VariantNumber != 2 {
		t.Fatalf("first = %+v", first)
	}
	if got := first.Resources; got != (ResourcesView{Known: true, Available: true, CPUMillicores: 400, MemoryBytes: 4096}) {
		t.Fatalf("first resources = %+v", got)
	}
	if page.Items[1].Resources.Known {
		t.Fatalf("a lab without usage has unknown resources: %+v", page.Items[1].Resources)
	}
	want := []struct {
		status  string
		expired bool
	}{{TestLabReady, false}, {TestLabFailed, true}, {TestLabCreating, false}, {TestLabUnknown, false}}
	for i, w := range want {
		if page.Items[i].Status != w.status || page.Items[i].Expired != w.expired {
			t.Errorf("item %d = status %q expired %v, want %q %v", i, page.Items[i].Status, page.Items[i].Expired, w.status, w.expired)
		}
	}
}

func TestListTestLabsWithoutAgentIsUnknown(t *testing.T) {
	uc, q := newTestLabs(t, nil, nil)
	q.EXPECT().ListPlatformTestLabs(gomock.Any(), gomock.Any()).Return([]postgres.ListPlatformTestLabsRow{{GroupName: "t-x", ExpiresAt: fixedNow.Add(time.Hour), Total: 1}}, nil)
	page, err := uc.ListTestLabs(context.Background(), "", 1, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != TestLabUnknown {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
}

func TestTerminateTestLabDestroysAsItsOwner(t *testing.T) {
	destroyer := &recordingDestroyer{}
	uc, q := newTestLabs(t, fakeLabAgent{}, destroyer)
	id, owner := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetPlatformTestLab(gomock.Any(), id).Return(postgres.GetPlatformTestLabRow{ID: id, GroupName: "t-x", CreatedBy: owner}, nil)
	if err := uc.TerminateTestLab(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if destroyer.owner != owner || destroyer.deploy != id {
		t.Fatalf("destroyed %v for %v, want %v for %v", destroyer.deploy, destroyer.owner, id, owner)
	}
}

func TestTerminateTestLabReportsAGoneLabAndMissingInfrastructure(t *testing.T) {
	uc, q := newTestLabs(t, fakeLabAgent{}, &recordingDestroyer{})
	q.EXPECT().GetPlatformTestLab(gomock.Any(), gomock.Any()).Return(postgres.GetPlatformTestLabRow{}, pgx.ErrNoRows)
	if err := uc.TerminateTestLab(context.Background(), uuid.Must(uuid.NewV7())); !errors.Is(err, infraModel.ErrTestLabNotFound.Err()) {
		t.Fatalf("err = %v, want not found", err)
	}
	none, _ := newTestLabs(t, nil, &recordingDestroyer{})
	if err := none.TerminateTestLab(context.Background(), uuid.Must(uuid.NewV7())); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestTotalResourcesSumsTheLabsThatReportUsage(t *testing.T) {
	agent := fakeLabAgent{statuses: map[string]exerciseModel.LabDeployStatus{
		"t-a": {Ready: true, UsageAvailable: true, CPUMillicores: 100, MemoryBytes: 10},
		"t-b": {Ready: true, UsageAvailable: true, CPUMillicores: 250, MemoryBytes: 20},
		"t-c": {Phase: exerciseModel.DeployPhaseProvisioning},
		"t-d": {Ready: true, UsageAvailable: true},
	}}
	uc, q := newTestLabs(t, agent, nil)
	row := func(group string) postgres.ListPlatformTestLabsRow {
		return postgres.ListPlatformTestLabsRow{GroupName: group, LabName: "lab", ExpiresAt: fixedNow.Add(time.Hour), Total: 3}
	}
	q.EXPECT().ListPlatformTestLabs(gomock.Any(), postgres.ListPlatformTestLabsParams{LimitVal: testLabsResourcesLimit}).Return([]postgres.ListPlatformTestLabsRow{row("t-a"), row("t-b"), row("t-c"), row("t-d")}, nil)
	got, err := uc.TotalResources(context.Background())
	if err != nil || got != (ResourcesView{Known: true, Available: true, CPUMillicores: 350, MemoryBytes: 30}) {
		t.Fatalf("total = %+v, err = %v", got, err)
	}
}

type deviceAgent struct {
	fakeLabAgent
	calls []string
	err   error
}

func (d *deviceAgent) ResetDevice(_ context.Context, group, lab, device string) error {
	d.calls = append(d.calls, "reset "+group+"/"+lab+"/"+device)
	return d.err
}

func (d *deviceAgent) RescueDevice(_ context.Context, group, lab, device string, enable bool) error {
	d.calls = append(d.calls, "rescue "+group+"/"+lab+"/"+device+"/"+map[bool]string{true: "on", false: "off"}[enable])
	return d.err
}

func TestListTestLabsShowsQueueAndImageWarning(t *testing.T) {
	agent := fakeLabAgent{statuses: map[string]exerciseModel.LabDeployStatus{
		"t-queued": {Phase: exerciseModel.DeployPhaseQueued, Queue: &exerciseModel.LabQueue{Position: 4, Length: 9, Reason: exerciseModel.QueueReasonInFlightLimit}, GroupImageWarning: "vpn:1"},
	}}
	uc, q := newTestLabs(t, agent, nil)
	q.EXPECT().ListPlatformTestLabs(gomock.Any(), gomock.Any()).Return([]postgres.ListPlatformTestLabsRow{{GroupName: "t-queued", ExpiresAt: fixedNow.Add(time.Hour), Total: 1}}, nil)
	page, err := uc.ListTestLabs(context.Background(), "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	item := page.Items[0]
	if item.Status != TestLabQueued || item.Queue == nil || item.Queue.Position != 4 || item.Queue.Length != 9 || !item.ImageWarning {
		t.Fatalf("item = %+v", item)
	}
}

func TestGetTestLabDetailReadsTheLiveLab(t *testing.T) {
	agent := fakeLabAgent{statuses: map[string]exerciseModel.LabDeployStatus{
		"t-x": {Phase: exerciseModel.DeployPhaseProvisioning, Devices: []exerciseModel.LabDeployedDevice{{Name: "web", Snapshot: &exerciseModel.DeviceSnapshot{SizeBytes: 3}}}},
	}}
	uc, q := newTestLabs(t, agent, nil)
	id := uuid.Must(uuid.NewV7())
	q.EXPECT().GetPlatformTestLab(gomock.Any(), id).Return(postgres.GetPlatformTestLabRow{ID: id, GroupName: "t-x", LabName: "lab-1"}, nil)
	detail, err := uc.GetTestLabDetail(context.Background(), id)
	if err != nil || detail.LabName != "lab-1" || detail.Status != TestLabCreating || detail.Live.Devices[0].Snapshot == nil {
		t.Fatalf("detail = %+v, err = %v", detail, err)
	}
	q.EXPECT().GetPlatformTestLab(gomock.Any(), id).Return(postgres.GetPlatformTestLabRow{}, pgx.ErrNoRows)
	if _, err = uc.GetTestLabDetail(context.Background(), id); !errors.Is(err, infraModel.ErrTestLabNotFound.Err()) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestTestLabDeviceActionsGoToTheLabOfTheTestLab(t *testing.T) {
	agent := &deviceAgent{}
	uc, q := newTestLabs(t, agent, nil)
	id := uuid.Must(uuid.NewV7())
	q.EXPECT().GetPlatformTestLab(gomock.Any(), id).Return(postgres.GetPlatformTestLabRow{ID: id, GroupName: "t-x", LabName: "lab-1"}, nil).Times(2)
	if err := uc.ResetTestLabDevice(context.Background(), id, "web"); err != nil {
		t.Fatal(err)
	}
	if err := uc.RescueTestLabDevice(context.Background(), id, "web", true); err != nil {
		t.Fatal(err)
	}
	if len(agent.calls) != 2 || agent.calls[0] != "reset t-x/lab-1/web" || agent.calls[1] != "rescue t-x/lab-1/web/on" {
		t.Fatalf("calls = %v", agent.calls)
	}
}

func TestTestLabDeviceActionsNeedAnAgentThatCanDoThem(t *testing.T) {
	uc, _ := newTestLabs(t, fakeLabAgent{}, nil)
	if err := uc.ResetTestLabDevice(context.Background(), uuid.Must(uuid.NewV7()), "web"); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	none, _ := newTestLabs(t, nil, nil)
	if err := none.RescueTestLabDevice(context.Background(), uuid.Must(uuid.NewV7()), "web", true); !errors.Is(err, infraModel.ErrInfrastructureUnavailable.Err()) {
		t.Fatalf("err = %v, want unavailable", err)
	}
}
