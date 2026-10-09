package labagent

import (
	"context"
	"fmt"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
)

func (c *Client) RetireLab(ctx context.Context, in eventLabModel.RetirementRequest) error {
	target, err := lifecycleTarget(in.StopTarget)
	if err != nil {
		return err
	}
	if in.OperationID == uuid.Nil || in.Revision <= in.StopTarget.Revision {
		return fmt.Errorf("retirement identity is incomplete")
	}
	out, err := c.RetireLabs(ctx, &labpb.RetireLabsRequest{Items: []*labpb.LabRetirementTarget{{StopTarget: target, RetirementOperationId: in.OperationID.String(), RetirementRevision: in.Revision}}})
	if err != nil {
		return agentErr("retire lab", err)
	}
	if len(out.GetResults()) != 1 || out.GetResults()[0].GetState() != labpb.ItemState_ITEM_STATE_UPDATED {
		return fmt.Errorf("lab retirement was not accepted")
	}
	return lifecycleResult("retire lab", in.StopTarget.Ref, out.GetResults())
}
func (c *Client) RetireLabGroup(ctx context.Context, in eventLabModel.GroupRetirementRequest) error {
	target, err := groupTarget(in.StopTarget)
	if err != nil {
		return err
	}
	if in.OperationID == uuid.Nil || in.Revision <= in.StopTarget.Revision {
		return fmt.Errorf("group retirement identity is incomplete")
	}
	out, err := c.RetireLabGroups(ctx, &labpb.RetireLabGroupsRequest{Items: []*labpb.GroupRetirementTarget{{StopTarget: target, RetirementOperationId: in.OperationID.String(), RetirementRevision: in.Revision}}})
	if err != nil {
		return agentErr("retire lab group", err)
	}
	return groupLifecycleResult(in.StopTarget.Group, out.GetResults())
}
func retirementObservation(r *labpb.RetirementStatus) *eventLabModel.RetirementObservation {
	if r == nil {
		return nil
	}
	return &eventLabModel.RetirementObservation{ExpectedUID: r.GetExpectedUid(), StopOperationID: uuid.FromStringOrNil(r.GetStopOperationId()), StopRevision: r.GetStopRevision(), OperationID: uuid.FromStringOrNil(r.GetOperationId()), Revision: r.GetRevision(), ObservedGeneration: r.GetObservedGeneration(), State: r.GetState(), ObservedAt: positiveMillis(r.GetObservedUnixMs()), RequestedAt: positiveMillis(r.GetRequestedUnixMs()), RuntimeAbsent: r.GetRuntimeAbsent(), StorageState: r.GetStorageState(), CleanupComplete: r.GetCleanupComplete(), PhysicalStorageBytesAvailable: r.GetPhysicalStorageBytesAvailable() && r.GetPhysicalStorageBytes() >= 0, PhysicalStorageBytes: r.GetPhysicalStorageBytes(), Error: r.GetError()}
}
func (f *Fleet) RetireLab(ctx context.Context, in eventLabModel.RetirementRequest) error {
	m, err := f.memberOf(ctx, in.StopTarget.Ref.Group)
	if err != nil {
		return err
	}
	return m.Client.RetireLab(ctx, in)
}
func (f *Fleet) RetireLabGroup(ctx context.Context, in eventLabModel.GroupRetirementRequest) error {
	m, err := f.memberOf(ctx, in.StopTarget.Group)
	if err != nil {
		return err
	}
	return m.Client.RetireLabGroup(ctx, in)
}
