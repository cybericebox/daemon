package labagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
)

// StopLab accepts one fenced intent. Only ObserveLab can certify physical stop.
func (c *Client) StopLab(ctx context.Context, in eventLabModel.StopRequest) error {
	if err := (eventLabModel.Policy{SnapshotMode: in.SnapshotMode}).Validate(); err != nil {
		return err
	}
	target, err := lifecycleTarget(in.Target)
	if err != nil {
		return err
	}
	feature, err := c.lifecycleFeature(ctx)
	if err != nil {
		return err
	}
	if !feature.GetPerLabStop() || !feature.GetConfirmedRuntime() || (in.SnapshotMode == "required" && !feature.GetRequiredSnapshot()) {
		return fmt.Errorf("stop lab: lifecycle capability unavailable")
	}
	mode := labpb.StopSnapshotMode_STOP_SNAPSHOT_MODE_SKIP
	if in.SnapshotMode == "required" {
		mode = labpb.StopSnapshotMode_STOP_SNAPSHOT_MODE_REQUIRED
	}
	until := int64(0)
	if in.RetentionUntil != nil {
		until = in.RetentionUntil.UnixMilli()
	}
	out, err := c.StopLabs(ctx, &labpb.StopLabsRequest{Items: []*labpb.StopLabItem{{Target: target, SnapshotMode: mode, RetentionUntilUnixMs: until, Terminal: in.Terminal}}})
	if err != nil {
		return agentErr("stop lab", err)
	}
	return lifecycleResult("stop lab", in.Target.Ref, out.GetResults())
}

func (c *Client) StartLab(ctx context.Context, in eventLabModel.Target) error {
	target, err := lifecycleTarget(in)
	if err != nil {
		return err
	}
	feature, err := c.lifecycleFeature(ctx)
	if err != nil {
		return err
	}
	if !feature.GetRetainedRestart() || !feature.GetConfirmedRuntime() {
		return fmt.Errorf("start lab: lifecycle capability unavailable")
	}
	out, err := c.StartLabs(ctx, &labpb.StartLabsRequest{Items: []*labpb.LabLifecycleTarget{target}})
	if err != nil {
		return agentErr("start lab", err)
	}
	return lifecycleResult("start lab", in.Ref, out.GetResults())
}

func lifecycleTarget(in eventLabModel.Target) (*labpb.LabLifecycleTarget, error) {
	if in.Ref.Group == "" || in.Ref.Lab == "" || in.ExpectedUID == "" || in.OperationID == uuid.Nil || in.Revision <= 0 {
		return nil, fmt.Errorf("lab lifecycle: incomplete identity target")
	}
	return &labpb.LabLifecycleTarget{Ref: &labpb.ItemRef{LabGroup: in.Ref.Group, Name: in.Ref.Lab}, OperationId: in.OperationID.String(), LifecycleRevision: in.Revision, ExpectedLabUid: in.ExpectedUID}, nil
}

// Unlike create/delete, a missing Lab is not acceptance of a stop or start.
// The legacy oneResult deliberately has different semantics for those calls.
func lifecycleResult(op string, ref eventLabModel.Ref, results []*labpb.ItemResult) error {
	if len(results) != 1 {
		return fmt.Errorf("%s: %d results for 1 item", op, len(results))
	}
	r := results[0]
	if r.GetRef().GetLabGroup() != ref.Group || r.GetRef().GetName() != ref.Lab || r.GetRef().GetLab() != "" {
		return fmt.Errorf("%s: mismatched item reference", op)
	}
	switch r.GetState() {
	case labpb.ItemState_ITEM_STATE_UPDATED, labpb.ItemState_ITEM_STATE_EXISTS:
		if r.GetError() == "" {
			return nil
		}
	case labpb.ItemState_ITEM_STATE_FAILED:
		if r.GetRetryable() {
			return &infraModel.TerminatingError{Message: fmt.Sprintf("%s: %s", op, r.GetError()), RetryAfter: infraModel.TerminatingRetryAfter, Err: errors.New(r.GetError())}
		}
	}
	return fmt.Errorf("%s: %s: %s", op, r.GetState(), r.GetError())
}

// ObserveLab reads exactly one existing reference. Missing definitions have no
// identity/release certificate; neither readiness nor absence frees capacity.
func (c *Client) ObserveLab(ctx context.Context, ref eventLabModel.Ref) (eventLabModel.Observation, error) {
	o := unknownObservation(ref)
	if ref.Group == "" || ref.Lab == "" {
		return o, fmt.Errorf("observe lab: incomplete reference")
	}
	list, err := c.ListLabs(ctx, &labpb.ListRequest{Items: []*labpb.ItemRef{{LabGroup: ref.Group, Name: ref.Lab}}})
	if err != nil {
		return o, agentErr("observe lab", err)
	}
	if len(list.GetItems()) == 0 {
		return o, nil
	}
	if len(list.GetItems()) != 1 {
		return o, fmt.Errorf("observe lab: %d results for 1 item", len(list.GetItems()))
	}
	l := list.GetItems()[0]
	if l.GetName() != ref.Lab || (l.GetLabGroupName() != "" && l.GetLabGroupName() != ref.Group) {
		return o, fmt.Errorf("observe lab: mismatched item reference")
	}
	return lifecycleObservation(ref, l), nil
}
func unknownObservation(ref eventLabModel.Ref) eventLabModel.Observation {
	return eventLabModel.Observation{Ref: ref, ActualState: "Unknown", SnapshotState: "Unknown", Allocation: eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}}
}
func lifecycleObservation(ref eventLabModel.Ref, l *labpb.Lab) eventLabModel.Observation {
	o := unknownObservation(ref)
	o.UID = l.GetUid()
	o.Generation = l.GetGeneration()
	s := l.GetStatus()
	life := s.GetLifecycle()
	if life == nil {
		o.DesiredState = "Running"
		return o
	}
	o.DesiredState = life.GetDesiredState()
	o.OperationID = uuid.FromStringOrNil(life.GetOperationId())
	o.Revision = life.GetLifecycleRevision()
	o.ObservedGeneration = life.GetObservedGeneration()
	// Live metadata and lifecycle status certify the same incarnation.
	if o.UID == "" || life.GetLabUid() != o.UID || o.Generation <= 0 || o.ObservedGeneration != o.Generation || o.OperationID == uuid.Nil || o.Revision <= 0 {
		return o
	}
	o.ActualState = knownState(life.GetObservedState(), "Running", "Snapshotting", "Stopping", "Stopped", "StopFailed", "Starting", "Deleted")
	o.FailureMessage = life.GetError()
	// Reason is generic producer context, not evidence of failure. Only an
	// explicit failed state or actual error belongs in the failure projection.
	if o.ActualState == "StopFailed" {
		o.FailureCode = life.GetReason()
		if o.FailureCode == "" {
			o.FailureCode = "StopFailed"
		}
	} else if o.FailureMessage != "" {
		o.FailureCode = "LifecycleError"
	}
	o.StoppedAt = positiveMillis(life.GetStoppedUnixMs())
	if life.GetSnapshotComplete() {
		o.SnapshotState = "Succeeded"
	} else if o.ActualState == "Snapshotting" {
		o.SnapshotState = "Snapshotting"
	} else if o.ActualState == "StopFailed" {
		o.SnapshotState = "Failed"
	}
	o.AccessFenced = life.GetAccessFenced() && life.GetAccessFencedUnixMs() > 0 && life.GetAccessFenceVpnBootId() != ""
	if o.AccessFenced {
		o.AccessFencedAt = positiveMillis(life.GetAccessFencedUnixMs())
		o.AccessFenceVPNBootID = life.GetAccessFenceVpnBootId()
	}
	o.RuntimeReady = o.DesiredState == "Running" && o.ActualState == "Running" && s.GetReady()
	a := s.GetResources()
	if a == nil || a.GetOperationId() != life.GetOperationId() || a.GetLifecycleRevision() != o.Revision || a.GetObservedUnixMs() <= 0 {
		return o
	}
	o.ObservedAt = positiveMillis(a.GetObservedUnixMs())
	o.Allocation = eventLabModel.Allocation{
		ConfiguredRequests:            computeOf(a.GetConfiguredRequests()),
		ConfiguredLimits:              computeOf(a.GetConfiguredLimits()),
		AllocatedRequests:             computeOf(a.GetAllocatedRequests()),
		Used:                          computeOf(a.GetUsed()),
		RuntimeState:                  knownState(a.GetRuntimeState(), "Allocated", "Releasing", "Released"),
		StorageState:                  knownState(a.GetStorageState(), "None", "Retained", "DeleteRequested", "CleanupPending", "Deleted"),
		ObservedAt:                    positiveMillis(a.GetObservedUnixMs()),
		UsageAvailable:                a.GetUsageAvailable(),
		SnapshotQuotaBytes:            a.GetSnapshotQuotaBytes(),
		PhysicalStorageBytesAvailable: a.GetPhysicalStorageBytesAvailable(),
		PhysicalStorageBytes:          a.GetPhysicalStorageBytes(),
	}
	if a.GetUsed() == nil || o.Allocation.Used.CPUMillicores < 0 || o.Allocation.Used.MemoryBytes < 0 {
		o.Allocation.UsageAvailable = false
	}
	if (o.Allocation.RuntimeState == "Allocated" || o.Allocation.RuntimeState == "Releasing") && a.GetAllocatedRequests() == nil {
		o.Allocation.RuntimeState = "Unknown"
		o.Allocation.UsageAvailable = false
	}
	// Resource certainty belongs to this operation after its access fence, and
	// must include an actual release timestamp and stopped observation.
	if o.Allocation.RuntimeState == "Released" {
		if !o.AccessFenced || a.GetObservedUnixMs() < life.GetAccessFencedUnixMs() || a.GetReleasedUnixMs() <= 0 || a.GetReleasedUnixMs() > a.GetObservedUnixMs() || a.GetReleasedUnixMs() < life.GetAccessFencedUnixMs() || (o.ActualState != "Stopped" && o.ActualState != "Deleted") || o.FailureCode != "" || o.FailureMessage != "" {
			o.Allocation.RuntimeState = "Unknown"
		} else {
			o.Allocation.ReleasedAt = positiveMillis(a.GetReleasedUnixMs())
		}
	}
	return o
}
func knownState(s string, values ...string) string {
	for _, v := range values {
		if s == v {
			return s
		}
	}
	return "Unknown"
}
func computeOf(a *labpb.ResourceAmounts) eventLabModel.Compute {
	return eventLabModel.Compute{CPUMillicores: a.GetCpuMillicores(), MemoryBytes: a.GetMemoryBytes()}
}
func positiveMillis(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms)
	return &t
}
