package labagent

import (
	"context"
	"fmt"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"time"
)

func groupTarget(t eventLabModel.GroupTarget) (*labpb.GroupTarget, error) {
	if t.Group == "" || t.ExpectedUID == "" || t.OperationID == uuid.Nil || t.Revision < 1 {
		return nil, fmt.Errorf("group lifecycle target is incomplete")
	}
	return &labpb.GroupTarget{Group: t.Group, ExpectedUid: t.ExpectedUID, OperationId: t.OperationID.String(), Revision: t.Revision}, nil
}
func (c *Client) StopLabGroup(ctx context.Context, t eventLabModel.GroupTarget) error {
	target, err := groupTarget(t)
	if err != nil {
		return err
	}
	feature, err := c.lifecycleFeature(ctx)
	if err != nil {
		return err
	}
	if !feature.GetFullGroupStop() || !feature.GetConfirmedRuntime() {
		return fmt.Errorf("full group stop is unavailable")
	}
	out, err := c.StopLabGroups(ctx, &labpb.StopLabGroupsRequest{Items: []*labpb.StopLabGroupItem{{Target: target, RequireAllLabsStopped: true}}})
	if err != nil {
		return agentErr("stop lab group", err)
	}
	return groupLifecycleResult(t.Group, out.GetResults())
}
func (c *Client) StartLabGroup(ctx context.Context, t eventLabModel.GroupTarget) error {
	target, err := groupTarget(t)
	if err != nil {
		return err
	}
	feature, err := c.lifecycleFeature(ctx)
	if err != nil {
		return err
	}
	if !feature.GetFullGroupStop() || !feature.GetConfirmedRuntime() || !feature.GetRetainedRestart() {
		return fmt.Errorf("full group restart is unavailable")
	}
	out, err := c.StartLabGroups(ctx, &labpb.StartLabGroupsRequest{Items: []*labpb.GroupTarget{target}})
	if err != nil {
		return agentErr("start lab group", err)
	}
	return groupLifecycleResult(t.Group, out.GetResults())
}
func groupLifecycleResult(group string, items []*labpb.ItemResult) error {
	if len(items) != 1 || items[0].GetRef().GetName() != group || items[0].GetRef().GetLabGroup() != "" || items[0].GetRef().GetLab() != "" || items[0].GetState() != labpb.ItemState_ITEM_STATE_UPDATED || items[0].GetError() != "" {
		return fmt.Errorf("group lifecycle request was not accepted for its exact target")
	}
	return nil
}
func (c *Client) ObserveLabGroup(ctx context.Context, group string) (eventLabModel.GroupObservation, error) {
	g, err := c.getGroup(ctx, group)
	if err != nil {
		return eventLabModel.GroupObservation{Name: group, ActualState: "Unknown"}, err
	}
	return groupLifecycleObservation(group, g, time.Now().UTC()), nil
}
func groupLifecycleObservation(name string, g *labpb.LabGroup, now time.Time) eventLabModel.GroupObservation {
	out := eventLabModel.GroupObservation{Name: name, UID: g.GetUid(), Generation: g.GetGeneration(), DesiredState: "Running", ActualState: "Unknown", Allocation: eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}}
	if g.GetName() != name || out.UID == "" || out.Generation <= 0 {
		return out
	}
	if projected, ok := any(g).(interface {
		GetVpnSize() *labpb.PodSize
		GetGatewaySize() *labpb.PodSize
	}); ok && projected.GetVpnSize() != nil && projected.GetGatewaySize() != nil {
		vpn, gateway := projected.GetVpnSize(), projected.GetGatewaySize()
		out.VPNSize = eventLabModel.Compute{CPUMillicores: vpn.GetCpuMillicores(), MemoryBytes: vpn.GetMemoryBytes()}
		out.GatewaySize = eventLabModel.Compute{CPUMillicores: gateway.GetCpuMillicores(), MemoryBytes: gateway.GetMemoryBytes()}
		out.ImmutableSizesKnown = out.VPNSize.CPUMillicores > 0 && out.VPNSize.MemoryBytes > 0 && out.GatewaySize.CPUMillicores > 0 && out.GatewaySize.MemoryBytes > 0
	}
	s := g.GetStatus()
	life := s.GetLifecycle()
	spec := g.GetLifecycle()
	bootAt := time.UnixMilli(s.GetCurrentVpnBootObservedUnixMs())
	network := s.GetCurrentVpnBootAvailable() && s.GetCurrentVpnBootId() != "" && !bootAt.After(now) && now.Sub(bootAt) <= 30*time.Second
	// Initial legacy creation may report readiness without a lifecycle command.
	// It grants no release credit and only revision1 consumes this exception.
	if spec == nil && (life == nil || (life.GetDesiredState() == "Running" && life.GetOperationId() == "" && life.GetLifecycleRevision() == 0)) {
		at := now
		out.ObservedAt = &at
		out.InitialReady = out.ImmutableSizesKnown && s.GetPhase() == "Ready" && s.GetNamespace() != "" && s.GetVpnRegistered() && !s.GetSuspended() && network
		out.Ready = out.InitialReady
		return out
	}
	if life == nil || spec == nil || life.GetLabUid() != out.UID || life.GetObservedGeneration() != out.Generation || life.GetLifecycleRevision() != spec.GetRevision() || life.GetOperationId() != spec.GetOperationId() || life.GetDesiredState() != spec.GetDesiredState() {
		return out
	}
	out.OperationID = uuid.FromStringOrNil(life.GetOperationId())
	out.Revision = life.GetLifecycleRevision()
	out.ObservedRevision = out.Revision
	out.ObservedGeneration = life.GetObservedGeneration()
	out.DesiredState = life.GetDesiredState()
	out.ActualState = knownState(life.GetObservedState(), "Running", "Starting", "Stopping", "Stopped", "StopFailed", "Deleted")
	out.AccessFenced = life.GetAccessFenced() && life.GetAccessFencedUnixMs() > 0 && life.GetAccessFenceVpnBootId() != ""
	out.FailureMessage = life.GetError()
	if out.FailureMessage != "" || out.ActualState == "StopFailed" {
		out.FailureCode = life.GetReason()
		if out.FailureCode == "" {
			out.FailureCode = "LifecycleError"
		}
	}
	a := s.GetResources()
	if a != nil && a.GetOperationId() == life.GetOperationId() && a.GetLifecycleRevision() == life.GetLifecycleRevision() && a.GetObservedUnixMs() > 0 {
		out.Allocation = resourceAllocation(a)
		out.ObservedAt = positiveMillis(a.GetObservedUnixMs())
		// A stopped group's services are absent, so no live VPN boot remains to
		// fence. Only its exact current native service-release certificate can
		// substitute for the per-lab VPN fencing evidence above.
		allocated := a.GetAllocatedRequests()
		if out.OperationID != uuid.Nil && out.Revision > 0 &&
			out.DesiredState == "Stopped" && out.ActualState == "Stopped" && spec.GetRequireAllLabsStopped() &&
			life.GetStoppedUnixMs() > 0 && life.GetReason() == "ServicesReleased" && out.FailureMessage == "" &&
			s.GetPhase() == "Suspended" && s.GetSuspended() && !s.GetVpnRegistered() &&
			a.GetRuntimeState() == "Released" && a.GetReleasedUnixMs() > 0 && allocated != nil &&
			allocated.GetCpuMillicores() == 0 && allocated.GetMemoryBytes() == 0 {
			out.AccessFenced = true
			out.ServiceReleaseCertified = true
		}
	}
	out.Ready = out.ActualState == "Running" && out.DesiredState == "Running" && s.GetPhase() == "Ready" && s.GetVpnRegistered() && !s.GetSuspended() && network
	out.Retirement = retirementObservation(s.GetRetirement())
	return out
}
func (f *Fleet) StopLabGroup(ctx context.Context, t eventLabModel.GroupTarget) error {
	m, err := f.memberOf(ctx, t.Group)
	if err != nil {
		return err
	}
	return m.Client.StopLabGroup(ctx, t)
}
func (f *Fleet) StartLabGroup(ctx context.Context, t eventLabModel.GroupTarget) error {
	m, err := f.memberOf(ctx, t.Group)
	if err != nil {
		return err
	}
	return m.Client.StartLabGroup(ctx, t)
}
func (f *Fleet) ObserveLabGroup(ctx context.Context, group string) (eventLabModel.GroupObservation, error) {
	m, err := f.memberOf(ctx, group)
	if err != nil {
		return eventLabModel.GroupObservation{Name: group, ActualState: "Unknown"}, err
	}
	return m.Client.ObserveLabGroup(ctx, group)
}

func resourceAllocation(a *labpb.ResourceAllocation) eventLabModel.Allocation {
	state := knownState(a.GetRuntimeState(), "Allocated", "Releasing", "Released")
	if (state == "Allocated" || state == "Releasing") && a.GetAllocatedRequests() == nil {
		state = "Unknown"
	}
	return eventLabModel.Allocation{ConfiguredRequests: computeOf(a.GetConfiguredRequests()), ConfiguredLimits: computeOf(a.GetConfiguredLimits()), AllocatedRequests: computeOf(a.GetAllocatedRequests()), Used: computeOf(a.GetUsed()), RuntimeState: state, StorageState: knownState(a.GetStorageState(), "None", "Retained", "DeleteRequested", "CleanupPending", "Deleted"), ObservedAt: positiveMillis(a.GetObservedUnixMs()), ReleasedAt: positiveMillis(a.GetReleasedUnixMs()), UsageAvailable: a.GetUsageAvailable() && a.GetUsed() != nil, SnapshotQuotaBytes: a.GetSnapshotQuotaBytes(), PhysicalStorageBytesAvailable: a.GetPhysicalStorageBytesAvailable(), PhysicalStorageBytes: a.GetPhysicalStorageBytes()}
}
