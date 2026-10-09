package eventLabObservationRepo

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

func (r *Repository) current(ctx context.Context, eventID, teamID uuid.UUID, group string) (*labpb.MonitoringUpdate, time.Time, error) {
	row, err := r.q.GetLabMonitoringCurrent(ctx, postgres.GetLabMonitoringCurrentParams{EventID: eventID, EventTeamID: teamID, LabGroupName: group})
	if err != nil {
		return nil, time.Time{}, err
	}
	out := &labpb.MonitoringUpdate{}
	if err = protojson.Unmarshal(row.Payload, out); err != nil {
		return nil, time.Time{}, err
	}
	return out, row.ObservedAt, nil
}
func (r *Repository) Group(ctx context.Context, eventID, teamID uuid.UUID, group string) (eventLabModel.GroupObservation, error) {
	out := eventLabModel.GroupObservation{Name: group, DesiredState: "Running", ActualState: "Unknown", Allocation: eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}}
	current, at, err := r.current(ctx, eventID, teamID, group)
	if err != nil {
		return out, err
	}
	for _, g := range current.GetGroups() {
		if g.GetName() != group {
			continue
		}
		out.UID = g.GetUid()
		s := g.GetStatus()
		life := s.GetLifecycle()
		if life == nil {
			return out, nil
		}
		if out.UID == "" || life.GetLabUid() != out.UID || g.GetGeneration() <= 0 || life.GetObservedGeneration() != g.GetGeneration() || life.GetLifecycleRevision() <= 0 || uuid.FromStringOrNil(life.GetOperationId()) == uuid.Nil {
			return out, nil
		}
		out.Revision = life.GetLifecycleRevision()
		out.ObservedRevision = out.Revision
		if desired := life.GetDesiredState(); desired == "Running" || desired == "Stopped" || desired == "Deleted" {
			out.DesiredState = desired
		}
		out.ActualState = knownGroupState(life.GetObservedState())
		out.ObservedAt = &at
		out.FailureMessage = life.GetError()
		if out.ActualState == "StopFailed" {
			out.FailureCode = life.GetReason()
		} else if out.FailureMessage != "" {
			out.FailureCode = "LifecycleError"
		}
		now := time.Now()
		bootAt := time.UnixMilli(s.GetCurrentVpnBootObservedUnixMs())
		currentNetwork := s.GetCurrentVpnBootAvailable() && s.GetCurrentVpnBootId() != "" && now.Sub(bootAt) >= 0 && now.Sub(bootAt) <= 30*time.Second
		out.Ready = out.ActualState == "Running" && out.DesiredState == "Running" && s.GetPhase() == "Ready" && s.GetVpnRegistered() && !s.GetSuspended() && currentNetwork && now.Sub(at) >= 0 && now.Sub(at) <= 30*time.Second
		a := s.GetResources()
		if a == nil || a.GetOperationId() != life.GetOperationId() || a.GetLifecycleRevision() != out.Revision {
			return out, nil
		}
		if a.GetObservedUnixMs() <= 0 {
			return out, nil
		}
		out.Allocation = allocationOf(a)
		if out.Allocation.RuntimeState == "Released" && (!life.GetAccessFenced() || life.GetAccessFencedUnixMs() <= 0 || life.GetAccessFenceVpnBootId() == "" || out.Allocation.ReleasedAt == nil || (out.ActualState != "Stopped" && out.ActualState != "Deleted") || out.FailureCode != "" || out.FailureMessage != "") {
			out.Allocation.RuntimeState = "Unknown"
			out.Allocation.ReleasedAt = nil
		}
		return out, nil
	}
	return out, nil
}
func allocationOf(a *labpb.ResourceAllocation) eventLabModel.Allocation {
	compute := func(v *labpb.ResourceAmounts) eventLabModel.Compute {
		return eventLabModel.Compute{CPUMillicores: v.GetCpuMillicores(), MemoryBytes: v.GetMemoryBytes()}
	}
	at := func(ms int64) *time.Time {
		if ms <= 0 {
			return nil
		}
		v := time.UnixMilli(ms)
		return &v
	}
	return eventLabModel.Allocation{ConfiguredRequests: compute(a.GetConfiguredRequests()), ConfiguredLimits: compute(a.GetConfiguredLimits()), AllocatedRequests: compute(a.GetAllocatedRequests()), Used: compute(a.GetUsed()), RuntimeState: a.GetRuntimeState(), StorageState: a.GetStorageState(), ObservedAt: at(a.GetObservedUnixMs()), ReleasedAt: at(a.GetReleasedUnixMs()), UsageAvailable: a.GetUsageAvailable(), SnapshotQuotaBytes: a.GetSnapshotQuotaBytes(), PhysicalStorageBytesAvailable: a.GetPhysicalStorageBytesAvailable(), PhysicalStorageBytes: a.GetPhysicalStorageBytes()}
}

func knownGroupState(state string) string {
	switch state {
	case "Running", "Snapshotting", "Stopping", "Stopped", "StopFailed", "Starting", "Deleting", "Deleted":
		return state
	default:
		return "Unknown"
	}
}
