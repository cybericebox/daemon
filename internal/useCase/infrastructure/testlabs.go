package infrastructure

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// Live states of a test lab as the agent reports them; unknown when the agent
// cannot be asked.
const (
	TestLabQueued   = "queued"
	TestLabCreating = "creating"
	TestLabReady    = "ready"
	TestLabFailed   = "failed"
	TestLabUnknown  = "unknown"
)

// testLabStatusLimit bounds how many labs of one page are asked for their live
// state at once.
const testLabStatusLimit = 8

type (
	// TestLabSource is the read side of the catalog test labs.
	TestLabSource interface {
		ListTestLabs(ctx context.Context, search string, limit, offset int32) ([]platformStandRepo.TestLab, int64, error)
		GetTestLab(ctx context.Context, id uuid.UUID) (platformStandRepo.TestLabRef, error)
	}

	// TestLabDestroyer tears a test lab down the way its author would (the exercise use case).
	TestLabDestroyer interface {
		DestroyDeployTest(ctx context.Context, userID, deployID uuid.UUID) error
	}

	// TestLabsUseCase lists the catalog test labs running on the infrastructure and
	// lets an admin end one of them.
	TestLabsUseCase struct {
		source    TestLabSource
		agent     Agent
		destroyer TestLabDestroyer
		now       func() time.Time
	}

	TestLabsDependencies struct {
		Source TestLabSource
		// Agent is nil when infrastructure is not configured.
		Agent     Agent
		Destroyer TestLabDestroyer
		Now       func() time.Time
	}

	// TestLabView is one catalog test lab with its author, exercise and live state.
	TestLabView struct {
		ID        uuid.UUID
		GroupName string
		// LabName is the Lab of this test lab inside its author's group.
		LabName       string
		ExerciseID    uuid.UUID
		ExerciseName  string
		VariantNumber int32
		AuthorID      uuid.UUID
		AuthorName    string
		AuthorEmail   string
		CreatedAt     time.Time
		ExpiresAt     time.Time
		// Expired is true when the lease is over but the lab is not cleaned up yet.
		Expired bool
		// Status is one of the TestLab* states.
		Status string
		// Resources is what the lab uses now; unknown while the agent has no usage for it.
		Resources ResourcesView
		// Queue is the place in the launch queue while the lab waits there (Status queued); nil otherwise.
		Queue *exerciseModel.LabQueue
		// ImageWarning: an image of the lab or its group is pulled by tag, not pinned to a digest.
		ImageWarning bool
	}

	// TestLabDetail is one test lab with its live state down to the devices and their snapshots.
	TestLabDetail struct {
		ID        uuid.UUID
		GroupName string
		LabName   string
		// Status is one of the TestLab* states.
		Status string
		Live   exerciseModel.LabDeployStatus
		// Topology is the exercise topology device behind every lab device, by the device's name in the lab.
		Topology map[string]TopologyDevice
	}

	// TopologyDevice is a device of the exercise topology: its logical name and type.
	TopologyDevice struct {
		Name string
		Type exerciseModel.DeviceType
	}

	TestLabsPage struct {
		Items    []TestLabView
		Total    int64
		Page     int
		PageSize int
	}
)

func NewTestLabsUseCase(deps TestLabsDependencies) *TestLabsUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &TestLabsUseCase{source: deps.Source, agent: deps.Agent, destroyer: deps.Destroyer, now: now}
}

// ListTestLabs returns one page of the catalog test labs, newest first. The live
// state comes from the agent; a lab the agent cannot answer for is "unknown".
func (u *TestLabsUseCase) ListTestLabs(ctx context.Context, search string, page, pageSize int) (TestLabsPage, error) {
	items, total, err := u.source.ListTestLabs(ctx, search, int32(pageSize), int32((page-1)*pageSize))
	if err != nil {
		return TestLabsPage{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list test laboratories").Err()
	}
	now := u.now()
	out := make([]TestLabView, len(items))
	for i, item := range items {
		out[i] = TestLabView{
			ID: item.ID, GroupName: item.GroupName, LabName: item.LabName, ExerciseID: item.ExerciseID, ExerciseName: item.ExerciseName, VariantNumber: item.VariantNumber,
			AuthorID: item.AuthorID, AuthorName: item.AuthorName, AuthorEmail: item.AuthorEmail,
			CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, Expired: !item.ExpiresAt.After(now), Status: TestLabUnknown,
		}
	}
	u.fillStatuses(ctx, out)
	return TestLabsPage{Items: out, Total: total, Page: page, PageSize: pageSize}, nil
}

// fillStatuses asks the agent for every lab of the page, a few at a time.
func (u *TestLabsUseCase) fillStatuses(ctx context.Context, views []TestLabView) {
	if u.agent == nil {
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, testLabStatusLimit)
	for i := range views {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			status, err := u.agent.LabStatus(ctx, views[i].GroupName, views[i].LabName)
			if err != nil {
				return
			}
			views[i].Status = testLabState(status)
			views[i].Resources = statusResources(status)
			// A lab whose pods are all dispatched (position 0) no longer waits, so it shows no queue.
			if status.Queue != nil && status.Queue.Position > 0 {
				views[i].Queue = status.Queue
			}
			views[i].ImageWarning = status.ImageWarning != "" || status.GroupImageWarning != ""
		}()
	}
	wg.Wait()
}

// statusResources is the live use of a lab; without a measurement it is unknown (nothing is requested-only here).
func statusResources(status exerciseModel.LabDeployStatus) ResourcesView {
	// Zero CPU and memory together are metrics that are not in yet, not a measurement.
	if !status.UsageAvailable || (status.CPUMillicores == 0 && status.MemoryBytes == 0) {
		return ResourcesView{}
	}
	return ResourcesView{Known: true, Available: true, CPUMillicores: status.CPUMillicores, MemoryBytes: status.MemoryBytes}
}

// testLabsResourcesLimit bounds how many test labs the platform total reads.
const testLabsResourcesLimit = 200

// TotalResources sums what the running catalog test labs use now.
func (u *TestLabsUseCase) TotalResources(ctx context.Context) (ResourcesView, error) {
	page, err := u.ListTestLabs(ctx, "", 1, testLabsResourcesLimit)
	if err != nil {
		return ResourcesView{}, err
	}
	var total ResourcesView
	for _, lab := range page.Items {
		total.Add(lab.Resources)
	}
	return total, nil
}

func testLabState(status exerciseModel.LabDeployStatus) string {
	switch {
	case status.Ready:
		return TestLabReady
	case status.Phase == exerciseModel.DeployPhaseFailed:
		return TestLabFailed
	case status.Phase == exerciseModel.DeployPhaseQueued:
		return TestLabQueued
	default:
		return TestLabCreating
	}
}

// TerminateTestLab ends a test lab on behalf of an admin: the lab group is
// destroyed and the lease removed, like when its author stops it.
func (u *TestLabsUseCase) TerminateTestLab(ctx context.Context, id uuid.UUID) error {
	if u.agent == nil {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	ref, err := u.source.GetTestLab(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return infraModel.ErrTestLabNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to load test laboratory").Err()
	}
	return u.destroyer.DestroyDeployTest(ctx, ref.OwnerID, ref.ID)
}

// GetTestLabDetail reads one test lab's live state for an administrator: the launch queue, every device
// with its snapshot state and the image warnings.
func (u *TestLabsUseCase) GetTestLabDetail(ctx context.Context, id uuid.UUID) (TestLabDetail, error) {
	if u.agent == nil {
		return TestLabDetail{}, infraModel.ErrInfrastructureUnavailable.Err()
	}
	ref, err := u.testLabRef(ctx, id)
	if err != nil {
		return TestLabDetail{}, err
	}
	status, err := u.agent.LabStatus(ctx, ref.GroupName, ref.LabName)
	if err != nil {
		return TestLabDetail{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read test laboratory status").Err()
	}
	return TestLabDetail{ID: ref.ID, GroupName: ref.GroupName, LabName: ref.LabName, Status: testLabState(status), Live: status, Topology: topologyByLabName(ref.Devices)}, nil
}

// topologyByLabName keys the topology devices by the name they have inside the lab.
func topologyByLabName(devices []exerciseModel.Device) map[string]TopologyDevice {
	out := make(map[string]TopologyDevice, len(devices))
	for i, name := range exerciseModel.LabDeviceNames(devices) {
		out[name] = TopologyDevice{Name: devices[i].Name, Type: devices[i].Type}
	}
	return out
}

// ResetTestLabDevice discards the snapshots of one device of a test lab and restarts it from its base image.
func (u *TestLabsUseCase) ResetTestLabDevice(ctx context.Context, id uuid.UUID, device string) error {
	ref, controller, err := u.deviceTarget(ctx, id)
	if err != nil {
		return err
	}
	return controller.ResetDevice(ctx, ref.GroupName, ref.LabName, device)
}

// RescueTestLabDevice starts one device of a test lab in rescue mode (enable) or back to normal.
func (u *TestLabsUseCase) RescueTestLabDevice(ctx context.Context, id uuid.UUID, device string, enable bool) error {
	ref, controller, err := u.deviceTarget(ctx, id)
	if err != nil {
		return err
	}
	return controller.RescueDevice(ctx, ref.GroupName, ref.LabName, device, enable)
}

func (u *TestLabsUseCase) deviceTarget(ctx context.Context, id uuid.UUID) (platformStandRepo.TestLabRef, infraModel.DeviceController, error) {
	if u.agent == nil {
		return platformStandRepo.TestLabRef{}, nil, infraModel.ErrInfrastructureUnavailable.Err()
	}
	controller, ok := u.agent.(infraModel.DeviceController)
	if !ok {
		return platformStandRepo.TestLabRef{}, nil, infraModel.ErrInfrastructureUnavailable.Err()
	}
	ref, err := u.testLabRef(ctx, id)
	if err != nil {
		return platformStandRepo.TestLabRef{}, nil, err
	}
	return ref, controller, nil
}

func (u *TestLabsUseCase) testLabRef(ctx context.Context, id uuid.UUID) (platformStandRepo.TestLabRef, error) {
	ref, err := u.source.GetTestLab(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return platformStandRepo.TestLabRef{}, infraModel.ErrTestLabNotFound.Err()
		}
		return platformStandRepo.TestLabRef{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load test laboratory").Err()
	}
	return ref, nil
}
