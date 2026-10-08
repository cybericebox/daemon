package labagent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Only transport is replaced; mapping, fencing and fleet placement are real.
type lifecycleAgent struct {
	*fakeAgent
	features   *labpb.FeaturesResponse
	featureErr error
	stop       *labpb.StopLabsRequest
	start      *labpb.StartLabsRequest
	list       *labpb.ListRequest
	results    []*labpb.ItemResult
}

func (a *lifecycleAgent) GetFeatures(context.Context, *labpb.Empty, ...grpc.CallOption) (*labpb.FeaturesResponse, error) {
	return a.features, a.featureErr
}
func (a *lifecycleAgent) StopLabs(_ context.Context, in *labpb.StopLabsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	a.stop = in
	return &labpb.BatchResult{Results: a.results}, a.callErr
}
func (a *lifecycleAgent) StartLabs(_ context.Context, in *labpb.StartLabsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	a.start = in
	return &labpb.BatchResult{Results: a.results}, a.callErr
}
func (a *lifecycleAgent) ListLabs(_ context.Context, in *labpb.ListRequest, _ ...grpc.CallOption) (*labpb.LabList, error) {
	a.list = in
	return &labpb.LabList{Items: a.labs}, a.listErr
}
func lifecycleFixture() (*Client, *lifecycleAgent, eventLabModel.Target) {
	a := &lifecycleAgent{fakeAgent: &fakeAgent{}, features: &labpb.FeaturesResponse{Lifecycle: &labpb.LifecycleFeature{PerLabStop: true, RequiredSnapshot: true, ConfirmedRuntime: true, RetainedRestart: true}}}
	target := eventLabModel.Target{Ref: eventLabModel.Ref{Group: "team", Lab: "shared"}, ExpectedUID: "uid-1", OperationID: uuid.FromStringOrNil("00000000-0000-0000-0000-000000000001"), Revision: 4}
	a.results = []*labpb.ItemResult{{Ref: &labpb.ItemRef{LabGroup: "team", Name: "shared"}, State: labpb.ItemState_ITEM_STATE_UPDATED}}
	return &Client{Client: a, instance: "prod"}, a, target
}
func TestLifecycleCommandsMapExactIdentityAndAcceptedDoesNotRelease(t *testing.T) {
	for _, mode := range []string{"skip", "required"} {
		t.Run(mode, func(t *testing.T) {
			c, a, target := lifecycleFixture()
			until := time.Unix(200, 123000000)
			a.labs = []*labpb.Lab{{Name: "shared", Uid: "uid-1", Generation: 7, Status: &labpb.LabStatus{Phase: "Ready", Ready: true}}}
			if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: mode, RetentionUntil: &until, Terminal: true}); err != nil {
				t.Fatal(err)
			}
			o, err := c.ObserveLab(context.Background(), target.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if o.ActualState != "Unknown" || o.Allocation.RuntimeState != "Unknown" || o.Allocation.ReleasedAt != nil {
				t.Fatalf("accepted stop inferred release: %+v", o)
			}
			items := a.stop.GetItems()
			if len(items) != 1 {
				t.Fatalf("items=%v", items)
			}
			got := items[0]
			wire := got.GetTarget()
			if wire.GetRef().GetLabGroup() != "team" || wire.GetRef().GetName() != "shared" || wire.GetRef().GetLab() != "" || wire.GetExpectedLabUid() != "uid-1" || wire.GetLifecycleRevision() != 4 || wire.GetOperationId() != "00000000-0000-0000-0000-000000000001" || !got.GetTerminal() || got.GetRetentionUntilUnixMs() != 200123 {
				t.Fatalf("incorrect stop: %v", got)
			}
			want := labpb.StopSnapshotMode_STOP_SNAPSHOT_MODE_SKIP
			if mode == "required" {
				want = labpb.StopSnapshotMode_STOP_SNAPSHOT_MODE_REQUIRED
			}
			if got.GetSnapshotMode() != want {
				t.Fatal(got)
			}
			if err := c.StartLab(context.Background(), target); err != nil {
				t.Fatal(err)
			}
			if len(a.start.GetItems()) != 1 || !reflect.DeepEqual(a.start.GetItems()[0], wire) {
				t.Fatalf("start=%v", a.start)
			}
		})
	}
}

func TestLifecycleCommandCapabilityAndTransportFailuresStayUnacknowledged(t *testing.T) {
	for _, field := range []string{"stop", "runtime", "restart"} {
		t.Run(field, func(t *testing.T) {
			c, a, target := lifecycleFixture()
			switch field {
			case "stop":
				a.features.Lifecycle.PerLabStop = false
			case "runtime":
				a.features.Lifecycle.ConfirmedRuntime = false
			case "restart":
				a.features.Lifecycle.RetainedRestart = false
			}
			var err error
			if field == "restart" {
				err = c.StartLab(context.Background(), target)
			} else {
				err = c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "skip"})
			}
			if err == nil || a.stop != nil || a.start != nil {
				t.Fatalf("sent unsupported operation: %v", err)
			}
		})
	}
	c, a, target := lifecycleFixture()
	a.featureErr = status.Error(codes.Unimplemented, "old agent")
	if err := c.StartLab(context.Background(), target); err == nil || a.start != nil {
		t.Fatal("old agent feature error ignored")
	}
	a.featureErr = nil
	a.callErr = status.Error(codes.Unavailable, "offline")
	if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "skip"}); err == nil {
		t.Fatal("RPC failure accepted")
	}
	for _, change := range []func(*eventLabModel.Target){func(t *eventLabModel.Target) { t.ExpectedUID = "" }, func(t *eventLabModel.Target) { t.OperationID = uuid.Nil }, func(t *eventLabModel.Target) { t.Revision = 0 }, func(t *eventLabModel.Target) { t.Ref.Lab = "" }} {
		c, a, target := lifecycleFixture()
		change(&target)
		if err := c.StartLab(context.Background(), target); err == nil || a.start != nil {
			t.Fatal("sent unfenced target")
		}
	}
	c, a, target = lifecycleFixture()
	if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "skip"}); err != nil {
		t.Fatal(err)
	}
	if a.stop.Items[0].Terminal || a.stop.Items[0].RetentionUntilUnixMs != 0 {
		t.Fatal("invented terminal or retention intent")
	}
}
func TestLifecycleCommandRejectsMalformedAndFailedResults(t *testing.T) {
	for _, state := range []labpb.ItemState{labpb.ItemState_ITEM_STATE_UNSPECIFIED, labpb.ItemState_ITEM_STATE_CREATED, labpb.ItemState_ITEM_STATE_DELETED, labpb.ItemState_ITEM_STATE_NOT_FOUND, labpb.ItemState_ITEM_STATE_FAILED} {
		t.Run(state.String(), func(t *testing.T) {
			c, a, target := lifecycleFixture()
			a.results[0].State = state
			a.results[0].Error = "refused"
			if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "skip"}); err == nil {
				t.Fatal("accepted unsupported result")
			}
		})
	}
	for _, change := range []func(*lifecycleAgent){func(a *lifecycleAgent) { a.results = nil }, func(a *lifecycleAgent) { a.results = append(a.results, a.results[0]) }, func(a *lifecycleAgent) { a.results[0].Ref.Name = "other" }, func(a *lifecycleAgent) { a.results[0].Error = "refused" }} {
		c, a, target := lifecycleFixture()
		change(a)
		if err := c.StartLab(context.Background(), target); err == nil {
			t.Fatal("accepted malformed result")
		}
	}
	c, a, target := lifecycleFixture()
	a.results[0].State = labpb.ItemState_ITEM_STATE_FAILED
	a.results[0].Retryable = true
	a.results[0].Error = "not ready"
	var retry *infraModel.TerminatingError
	if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "skip"}); !errors.As(err, &retry) {
		t.Fatalf("retry=%v", err)
	}
	c, a, target = lifecycleFixture()
	a.results[0].State = labpb.ItemState_ITEM_STATE_EXISTS
	if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "skip"}); err != nil {
		t.Fatal(err)
	}
	if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: ""}); err == nil {
		t.Fatal("guessed snapshot policy")
	}
}
func releasedWire(target eventLabModel.Target) *labpb.Lab {
	return &labpb.Lab{Name: "shared", Uid: "uid-1", Generation: 9, Status: &labpb.LabStatus{Phase: "Stopped", Ready: false, Lifecycle: &labpb.LabLifecycleStatus{LabUid: "uid-1", DesiredState: "Stopped", ObservedState: "Stopped", OperationId: target.OperationID.String(), LifecycleRevision: 4, ObservedGeneration: 9, SnapshotComplete: true, StoppedUnixMs: 200000, AccessFenced: true, AccessFencedUnixMs: 190000, AccessFenceVpnBootId: "boot-1"}, Resources: &labpb.ResourceAllocation{OperationId: target.OperationID.String(), LifecycleRevision: 4, RuntimeState: "Released", StorageState: "Retained", ConfiguredRequests: &labpb.ResourceAmounts{CpuMillicores: 750, MemoryBytes: 512}, ConfiguredLimits: &labpb.ResourceAmounts{CpuMillicores: 1000, MemoryBytes: 1024}, AllocatedRequests: &labpb.ResourceAmounts{}, ObservedUnixMs: 210000, ReleasedUnixMs: 200000, UsageAvailable: false, SnapshotQuotaBytes: 2048, PhysicalStorageBytesAvailable: true, PhysicalStorageBytes: 1000}}}
}
func TestLifecycleObservationMapsActualCertificate(t *testing.T) {
	c, a, target := lifecycleFixture()
	a.labs = []*labpb.Lab{releasedWire(target)}
	o, err := c.ObserveLab(context.Background(), target.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.list.GetItems()) != 1 || a.list.GetSelector() != "" || a.list.GetItems()[0].GetLabGroup() != "team" || a.list.GetItems()[0].GetName() != "shared" {
		t.Fatal(a.list)
	}
	if o.Ref != target.Ref || o.UID != "uid-1" || o.Generation != 9 || o.ObservedGeneration != 9 || o.OperationID != target.OperationID || o.Revision != 4 || o.ActualState != "Stopped" || o.DesiredState != "Stopped" || o.SnapshotState != "Succeeded" || !o.AccessFenced || o.AccessFencedAt.UnixMilli() != 190000 || o.AccessFenceVPNBootID != "boot-1" || o.ObservedAt.UnixMilli() != 210000 || o.StoppedAt.UnixMilli() != 200000 {
		t.Fatalf("observation=%+v", o)
	}
	if o.Allocation.RuntimeState != "Released" || o.Allocation.StorageState != "Retained" || o.Allocation.ConfiguredRequests.CPUMillicores != 750 || o.Allocation.ConfiguredLimits.MemoryBytes != 1024 || o.Allocation.SnapshotQuotaBytes != 2048 || !o.Allocation.PhysicalStorageBytesAvailable || o.Allocation.PhysicalStorageBytes != 1000 || o.Allocation.ReleasedAt.UnixMilli() != 200000 {
		t.Fatalf("allocation=%+v", o.Allocation)
	}
}
func TestLifecycleObservationCannotCreditStaleOrMissingRuntime(t *testing.T) {
	for name, change := range map[string]func(*labpb.Lab){
		"missing lifecycle": func(l *labpb.Lab) { l.Status.Lifecycle = nil }, "missing resources": func(l *labpb.Lab) { l.Status.Resources = nil }, "wrong lifecycle UID": func(l *labpb.Lab) { l.Status.Lifecycle.LabUid = "other" }, "stale live generation": func(l *labpb.Lab) { l.Generation = 10 }, "wrong resource op": func(l *labpb.Lab) { l.Status.Resources.OperationId = "other" }, "wrong resource revision": func(l *labpb.Lab) { l.Status.Resources.LifecycleRevision = 3 }, "missing resource time": func(l *labpb.Lab) { l.Status.Resources.ObservedUnixMs = 0 }, "pre-fence resource time": func(l *labpb.Lab) { l.Status.Resources.ObservedUnixMs = 180000 }, "missing fence": func(l *labpb.Lab) { l.Status.Lifecycle.AccessFenced = false }, "missing fence time": func(l *labpb.Lab) { l.Status.Lifecycle.AccessFencedUnixMs = 0 }, "switch-only": func(l *labpb.Lab) { l.Status.Resources.RuntimeState = "Unknown" }, "missing release time": func(l *labpb.Lab) { l.Status.Resources.ReleasedUnixMs = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c, a, target := lifecycleFixture()
			wire := releasedWire(target)
			change(wire)
			a.labs = []*labpb.Lab{wire}
			o, err := c.ObserveLab(context.Background(), target.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if o.Allocation.RuntimeState == "Released" || o.Allocation.ReleasedAt != nil {
				t.Fatalf("credited stale release: %+v", o)
			}
		})
	}
}
func TestLifecycleObservationPreservesInitialIdentityAndRawStatus(t *testing.T) {
	c, a, target := lifecycleFixture()
	a.labs = []*labpb.Lab{{Name: "shared", Uid: "initial", Generation: 7, Status: &labpb.LabStatus{Phase: "Ready", Ready: true, Access: []*labpb.LabAccessEntry{{Device: "web", Port: 80, Url: "https://lab"}}}}}
	o, err := c.ObserveLab(context.Background(), target.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if o.UID != "initial" || o.Generation != 7 || o.ObservedGeneration != 0 || o.OperationID != uuid.Nil || o.ActualState != "Unknown" || o.Allocation.RuntimeState != "Unknown" {
		t.Fatalf("initial=%+v", o)
	}
	raw := mapLabStatus(a.labs[0])
	if raw.Phase != "Ready" || !raw.Ready || raw.Access[0].URL != "https://lab" || raw.LabUID != "initial" || raw.LabGeneration != 7 {
		t.Fatalf("raw=%+v", raw)
	}
	a.labs[0].Status = nil
	raw = mapLabStatus(a.labs[0])
	if raw.LabUID != "initial" || raw.LabGeneration != 7 || raw.Phase != "Pending" {
		t.Fatalf("unobserved live identity lost: %+v", raw)
	}
	a.labs = nil
	o, err = c.ObserveLab(context.Background(), target.Ref)
	if err != nil || o.Allocation.RuntimeState != "Unknown" || o.UID != "" {
		t.Fatalf("missing=%+v err=%v", o, err)
	}
	a.labs = []*labpb.Lab{{Name: "other"}}
	if _, err = c.ObserveLab(context.Background(), target.Ref); err == nil {
		t.Fatal("accepted foreign reference")
	}
	a.listErr = errors.New("unreachable")
	if _, err = c.ObserveLab(context.Background(), target.Ref); err == nil {
		t.Fatal("ignored unreachable agent")
	}
}

func TestLifecycleAdapterAndDomainKeepHeldAllocationUntilExactCurrentRelease(t *testing.T) {
	for name, change := range map[string]func(*labpb.Lab){"current": func(*labpb.Lab) {}, "wrong operation": func(l *labpb.Lab) {
		l.Status.Lifecycle.OperationId = "00000000-0000-0000-0000-000000000002"
		l.Status.Resources.OperationId = l.Status.Lifecycle.OperationId
	}, "wrong revision": func(l *labpb.Lab) { l.Status.Lifecycle.LifecycleRevision = 3; l.Status.Resources.LifecycleRevision = 3 }, "replacement UID": func(l *labpb.Lab) { l.Uid = "replacement"; l.Status.Lifecycle.LabUid = "replacement" }, "missing resources": func(l *labpb.Lab) { l.Status.Resources = nil }, "stale resource": func(l *labpb.Lab) { l.Status.Resources.LifecycleRevision = 3 }, "stale generation": func(l *labpb.Lab) { l.Generation = 10 }, "unknown": func(l *labpb.Lab) { l.Status.Resources.RuntimeState = "Unknown" }, "snapshot failed": func(l *labpb.Lab) {
		l.Status.Lifecycle.SnapshotComplete = false
		l.Status.Lifecycle.ObservedState = "StopFailed"
		l.Status.Lifecycle.Reason = "CaptureFailed"
		l.Status.Lifecycle.Error = "capture refused"
	}} {
		t.Run(name, func(t *testing.T) {
			c, a, target := lifecycleFixture()
			wire := releasedWire(target)
			change(wire)
			a.labs = []*labpb.Lab{wire}
			lab := eventLabModel.Lab{Ref: target.Ref, AgentUID: "uid-1", AgentGeneration: 7, Revision: 4, OperationID: target.OperationID, DesiredState: "Stopped", ActualState: "Running", SnapshotMode: "required", Allocation: eventLabModel.Allocation{RuntimeState: "Allocated", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512}, SnapshotQuotaBytes: 2048, StorageState: "Retained"}}
			o, err := c.ObserveLab(context.Background(), target.Ref)
			if err != nil {
				t.Fatal(err)
			}
			lab.Observe(o, time.Unix(211, 0))
			if name == "current" {
				if lab.Allocation.RuntimeState != "Released" || lab.Allocation.AllocatedRequests.MemoryBytes != 0 || lab.AgentGeneration != 9 {
					t.Fatalf("certificate refused: %+v", lab)
				}
			} else if lab.Allocation.RuntimeState == "Released" || lab.Allocation.AllocatedRequests.MemoryBytes != 512 || lab.Allocation.SnapshotQuotaBytes != 2048 {
				t.Fatalf("unproven release changed held allocation: %+v", lab)
			}
		})
	}
}

func TestLifecycleInformationalReasonDoesNotBlockHealthyRelease(t *testing.T) {
	for _, tc := range []struct {
		name, state, reason, producerError, failureCode string
		released                                        bool
	}{
		{name: "healthy blank reason", state: "Stopped", released: true},
		{name: "healthy informational reason", state: "Stopped", reason: "RuntimeConfirmed", released: true},
		{name: "failed stop reason", state: "StopFailed", reason: "CaptureFailed", failureCode: "CaptureFailed"},
		{name: "failed stop without reason", state: "StopFailed", failureCode: "StopFailed"},
		{name: "actual error with informational reason", state: "Stopped", reason: "RuntimeConfirmed", producerError: "runtime confirmation failed", failureCode: "LifecycleError"},
		{name: "actual error without reason", state: "Stopped", producerError: "runtime confirmation failed", failureCode: "LifecycleError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, a, target := lifecycleFixture()
			wire := releasedWire(target)
			wire.Status.Lifecycle.ObservedState = tc.state
			wire.Status.Lifecycle.Reason = tc.reason
			wire.Status.Lifecycle.Error = tc.producerError
			a.labs = []*labpb.Lab{wire}
			lab := eventLabModel.Lab{Ref: target.Ref, AgentUID: "uid-1", AgentGeneration: 7, Revision: 4, OperationID: target.OperationID, DesiredState: "Stopped", ActualState: "Running", SnapshotMode: "required", Allocation: eventLabModel.Allocation{RuntimeState: "Allocated", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512}, SnapshotQuotaBytes: 2048, StorageState: "Retained"}}
			o, err := c.ObserveLab(context.Background(), target.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if !lab.Observe(o, time.Unix(211, 0)) {
				t.Fatal("current observation did not reach domain ledger")
			}
			if tc.released {
				if lab.ActualState != "Stopped" || lab.Allocation.RuntimeState != "Released" || lab.Allocation.ReleasedAt == nil || lab.Allocation.AllocatedRequests != (eventLabModel.Compute{}) || lab.FailureCode != "" || lab.FailureMessage != "" {
					t.Fatalf("healthy certificate refused: observation=%+v lab=%+v", o, lab)
				}
			} else if lab.Allocation.RuntimeState == "Released" || lab.Allocation.ReleasedAt != nil || lab.Allocation.AllocatedRequests != (eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512}) || lab.Allocation.SnapshotQuotaBytes != 2048 {
				t.Fatalf("failed stop/error released held allocation: %+v", lab)
			}
			if o.FailureCode != tc.failureCode || o.FailureMessage != tc.producerError {
				t.Fatalf("generic reason misclassified: observation=%+v", o)
			}
		})
	}
}

func TestLifecycleMissingAllocatedAmountsCannotEraseHeldCompute(t *testing.T) {
	c, a, target := lifecycleFixture()
	wire := releasedWire(target)
	wire.Status.Resources.RuntimeState = "Allocated"
	wire.Status.Resources.AllocatedRequests = nil
	a.labs = []*labpb.Lab{wire}
	o, err := c.ObserveLab(context.Background(), target.Ref)
	if err != nil {
		t.Fatal(err)
	}
	l := eventLabModel.Lab{Ref: target.Ref, AgentUID: "uid-1", AgentGeneration: 7, Revision: 4, OperationID: target.OperationID, DesiredState: "Stopped", Allocation: eventLabModel.Allocation{RuntimeState: "Allocated", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512}}}
	l.Observe(o, time.Unix(211, 0))
	if l.Allocation.AllocatedRequests.MemoryBytes != 512 || l.Allocation.AllocatedRequests.CPUMillicores != 750 {
		t.Fatalf("missing measurement erased held compute: %+v", l.Allocation)
	}
}

func TestLifecycleMissingUsageAmountsAreUnavailable(t *testing.T) {
	c, a, target := lifecycleFixture()
	wire := releasedWire(target)
	wire.Status.Resources.RuntimeState = "Allocated"
	wire.Status.Resources.AllocatedRequests = &labpb.ResourceAmounts{CpuMillicores: 750, MemoryBytes: 512}
	wire.Status.Resources.UsageAvailable = true
	wire.Status.Resources.Used = nil
	a.labs = []*labpb.Lab{wire}
	o, err := c.ObserveLab(context.Background(), target.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if o.Allocation.UsageAvailable {
		t.Fatal("claimed known zero usage from absent measurement")
	}
}

func TestLifecycleFleetPreparationChecksThePlacedAgentBeforeProvisioning(t *testing.T) {
	c, a, _ := lifecycleFixture()
	other, b, _ := lifecycleFixture()
	b.features.Lifecycle = nil
	am := &Member{ID: uuid.Must(uuid.NewV7()), Client: c, Enabled: true}
	bm := &Member{ID: uuid.Must(uuid.NewV7()), Client: other, Enabled: true}
	store := &memoryPlacements{groups: map[string]uuid.UUID{"team": bm.ID}}
	f := NewFleet(store, nil, am, bm)
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{{Type: exerciseModel.DeviceTypeContainer, Persistence: &exerciseModel.DevicePersistence{Enabled: true}}}}
	if err := f.RequireLabLifecyclePreparation(context.Background(), "team", eventLabModel.Policy{SnapshotMode: "required"}, topo); err == nil {
		t.Fatal("another supported agent excused the actual placed agent")
	}
	if a.createLabs != nil || b.createLabs != nil || a.createGroup != nil || b.createGroup != nil {
		t.Fatal("preparation provisioned workloads")
	}
	b.features.Lifecycle = &labpb.LifecycleFeature{PerLabStop: true, ConfirmedRuntime: true, RequiredSnapshot: true}
	if err := f.RequireLabLifecyclePreparation(context.Background(), "team", eventLabModel.Policy{SnapshotMode: "required"}, topo); err != nil {
		t.Fatal(err)
	}
}
func TestLifecycleCapabilityUsesActualFeatureAndPreparationTopology(t *testing.T) {
	topo := exerciseModel.Topology{Devices: []exerciseModel.Device{{Type: exerciseModel.DeviceTypeContainer, Persistence: &exerciseModel.DevicePersistence{Enabled: true}}}}
	for name, features := range map[string]*labpb.LifecycleFeature{"old": nil, "unsupported": {}, "no capture": {PerLabStop: true, ConfirmedRuntime: true}, "no runtime": {PerLabStop: true, RequiredSnapshot: true}, "no stop": {RequiredSnapshot: true, ConfirmedRuntime: true}} {
		t.Run(name, func(t *testing.T) {
			c, a, target := lifecycleFixture()
			a.features.Lifecycle = features
			if err := c.RequireLabLifecyclePreparation(context.Background(), target.Ref.Group, eventLabModel.Policy{SnapshotMode: "required"}, topo); err == nil {
				t.Fatal("required preparation accepted unsupported feature")
			}
			if err := c.StopLab(context.Background(), eventLabModel.StopRequest{Target: target, SnapshotMode: "required"}); err == nil {
				t.Fatal("required stop accepted unsupported feature")
			}
			if a.stop != nil {
				t.Fatal("sent unsupported command")
			}
		})
	}
	c, _, target := lifecycleFixture()
	if err := c.RequireLabLifecyclePreparation(context.Background(), target.Ref.Group, eventLabModel.Policy{SnapshotMode: "required"}, topo); err != nil {
		t.Fatal(err)
	}
	topo.Devices[0].Persistence = nil
	if err := c.RequireLabLifecyclePreparation(context.Background(), target.Ref.Group, eventLabModel.Policy{SnapshotMode: "required"}, topo); err == nil {
		t.Fatal("accepted missing persistence")
	}
}
func TestLifecycleFleetObserveNeverClaimsOrRetiresPlacement(t *testing.T) {
	c, a, target := lifecycleFixture()
	other, b, _ := lifecycleFixture()
	a.labs = []*labpb.Lab{releasedWire(target)}
	am := &Member{ID: uuid.Must(uuid.NewV7()), Client: c}
	bm := &Member{ID: uuid.Must(uuid.NewV7()), Client: other}
	store := &memoryPlacements{groups: map[string]uuid.UUID{"team": am.ID}}
	f := NewFleet(store, nil, am, bm)
	if _, err := f.ObserveLab(context.Background(), target.Ref); err != nil {
		t.Fatal(err)
	}
	if b.list != nil || len(store.released) != 0 || len(store.groups) != 1 {
		t.Fatal("changed placement")
	}
	delete(store.groups, "team")
	if o, err := f.ObserveLab(context.Background(), target.Ref); err == nil || o.Allocation.RuntimeState != "Unknown" || len(store.groups) != 0 {
		t.Fatalf("created placement on read: %+v %v", o, err)
	}
}

func TestLifecycleObserveRejectsIncompleteReferenceBeforeListing(t *testing.T) {
	for _, ref := range []eventLabModel.Ref{{Group: "team"}, {Lab: "shared"}, {}} {
		c, a, _ := lifecycleFixture()
		if _, err := c.ObserveLab(context.Background(), ref); err == nil || a.list != nil {
			t.Fatalf("listed incomplete reference: %+v %v", ref, err)
		}
	}
}
