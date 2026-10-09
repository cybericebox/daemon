package labagent

import (
	"testing"
	"time"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"google.golang.org/protobuf/proto"
)

func stoppedGroupServiceCertificate() (*labpb.LabGroup, time.Time) {
	now := time.UnixMilli(1791573546000)
	return &labpb.LabGroup{
		Name: "moderator-group", Uid: "d5ba1eac-979d-4654-9262-875448249ca6", Generation: 7,
		VpnSize:     &labpb.PodSize{CpuMillicores: 100, MemoryBytes: 335544320},
		GatewaySize: &labpb.PodSize{CpuMillicores: 10, MemoryBytes: 33554432},
		Lifecycle:   &labpb.GroupLifecycleSpec{DesiredState: "Stopped", OperationId: "01a1221a-a463-7c8d-93e6-d7d9ee1b4150", Revision: 2, RequireAllLabsStopped: true},
		Status: &labpb.LabGroupStatus{Phase: "Suspended", Suspended: true, VpnRegistered: false,
			Lifecycle: &labpb.LabLifecycleStatus{DesiredState: "Stopped", ObservedState: "Stopped", OperationId: "01a1221a-a463-7c8d-93e6-d7d9ee1b4150", LifecycleRevision: 2, LabUid: "d5ba1eac-979d-4654-9262-875448249ca6", ObservedGeneration: 7, Reason: "ServicesReleased", StoppedUnixMs: 1791573545000},
			Resources: &labpb.ResourceAllocation{ConfiguredRequests: &labpb.ResourceAmounts{CpuMillicores: 110, MemoryBytes: 369098752}, ConfiguredLimits: &labpb.ResourceAmounts{CpuMillicores: 110, MemoryBytes: 369098752}, AllocatedRequests: &labpb.ResourceAmounts{}, RuntimeState: "Released", ObservedUnixMs: 1791573545000, ReleasedUnixMs: 1791573545000, OperationId: "01a1221a-a463-7c8d-93e6-d7d9ee1b4150", LifecycleRevision: 2},
		},
	}, now
}

func groupLedgerForCertificate(g *labpb.LabGroup) eventLabModel.Group {
	previous := eventLabModel.Compute{CPUMillicores: 140, MemoryBytes: 603979776}
	return eventLabModel.Group{Name: g.Name, AgentUID: g.Uid, AgentGeneration: 6, Revision: 2, OperationID: uuid.FromStringOrNil(g.Lifecycle.OperationId), DesiredState: "Stopped", ActualState: "Unknown", ConfiguredRequests: previous, Allocation: eventLabModel.Allocation{RuntimeState: "Admitted", AllocatedRequests: previous, ConfiguredRequests: previous}}
}

func TestGroupServiceReleaseCertificateClearsHeldComputeWithoutVPNBoot(t *testing.T) {
	g, now := stoppedGroupServiceCertificate()
	observation := groupLifecycleObservation(g.Name, g, now)
	if !observation.AccessFenced {
		t.Fatal("exact group service absence must fence access without inventing a vanished VPN boot")
	}
	ledger := groupLedgerForCertificate(g)
	previous := ledger.ConfiguredRequests
	if !ledger.Observe(observation, now) || ledger.HeldCompute() != (eventLabModel.Compute{}) {
		t.Fatalf("exact stopped/released group remains held: %+v", ledger)
	}
	if ledger.Ready || ledger.ConfiguredRequests != previous || ledger.Allocation.AllocatedRequests != previous {
		t.Fatal("release must preserve historical sizing and never grant runtime readiness")
	}
}

func TestGroupServiceReleaseCertificateRejectsIncompleteOrForeignProof(t *testing.T) {
	cases := map[string]func(*labpb.LabGroup){
		"missing_uid":        func(g *labpb.LabGroup) { g.Uid = "" },
		"wrong_name":         func(g *labpb.LabGroup) { g.Name = "another-group" },
		"missing_spec":       func(g *labpb.LabGroup) { g.Lifecycle = nil },
		"missing_lifecycle":  func(g *labpb.LabGroup) { g.Status.Lifecycle = nil },
		"legacy_suspended":   func(g *labpb.LabGroup) { g.Lifecycle = nil; g.Status.Lifecycle = nil },
		"wrong_uid":          func(g *labpb.LabGroup) { g.Status.Lifecycle.LabUid = "other-uid" },
		"wrong_generation":   func(g *labpb.LabGroup) { g.Status.Lifecycle.ObservedGeneration-- },
		"missing_generation": func(g *labpb.LabGroup) { g.Generation = 0; g.Status.Lifecycle.ObservedGeneration = 0 },
		"invalid_operation": func(g *labpb.LabGroup) {
			g.Lifecycle.OperationId = "invalid"
			g.Status.Lifecycle.OperationId = "invalid"
			g.Status.Resources.OperationId = "invalid"
		},
		"wrong_operation": func(g *labpb.LabGroup) { g.Status.Lifecycle.OperationId = "01a1221a-a463-7c8d-93e6-d7d9ee1b4151" },
		"wrong_revision":  func(g *labpb.LabGroup) { g.Status.Lifecycle.LifecycleRevision++ },
		"missing_revision": func(g *labpb.LabGroup) {
			g.Lifecycle.Revision = 0
			g.Status.Lifecycle.LifecycleRevision = 0
			g.Status.Resources.LifecycleRevision = 0
		},
		"running_intent": func(g *labpb.LabGroup) {
			g.Lifecycle.DesiredState = "Running"
			g.Status.Lifecycle.DesiredState = "Running"
		},
		"children_not_required":    func(g *labpb.LabGroup) { g.Lifecycle.RequireAllLabsStopped = false },
		"not_stopped":              func(g *labpb.LabGroup) { g.Status.Lifecycle.ObservedState = "Stopping" },
		"missing_stop_time":        func(g *labpb.LabGroup) { g.Status.Lifecycle.StoppedUnixMs = 0 },
		"wrong_reason":             func(g *labpb.LabGroup) { g.Status.Lifecycle.Reason = "WaitingForNativeServiceRelease" },
		"error":                    func(g *labpb.LabGroup) { g.Status.Lifecycle.Error = "incomplete native reports" },
		"wrong_phase":              func(g *labpb.LabGroup) { g.Status.Phase = "Ready" },
		"not_suspended":            func(g *labpb.LabGroup) { g.Status.Suspended = false },
		"vpn_registered":           func(g *labpb.LabGroup) { g.Status.VpnRegistered = true },
		"missing_resources":        func(g *labpb.LabGroup) { g.Status.Resources = nil },
		"wrong_resource_operation": func(g *labpb.LabGroup) { g.Status.Resources.OperationId = "foreign" },
		"wrong_resource_revision":  func(g *labpb.LabGroup) { g.Status.Resources.LifecycleRevision++ },
		"missing_observation":      func(g *labpb.LabGroup) { g.Status.Resources.ObservedUnixMs = 0 },
		"missing_release_time":     func(g *labpb.LabGroup) { g.Status.Resources.ReleasedUnixMs = 0 },
		"allocated_state":          func(g *labpb.LabGroup) { g.Status.Resources.RuntimeState = "Allocated" },
		"missing_allocated":        func(g *labpb.LabGroup) { g.Status.Resources.AllocatedRequests = nil },
		"allocated_cpu":            func(g *labpb.LabGroup) { g.Status.Resources.AllocatedRequests.CpuMillicores = 1 },
		"allocated_memory":         func(g *labpb.LabGroup) { g.Status.Resources.AllocatedRequests.MemoryBytes = 1 },
		"negative_allocated":       func(g *labpb.LabGroup) { g.Status.Resources.AllocatedRequests.MemoryBytes = -1 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			original, now := stoppedGroupServiceCertificate()
			g := proto.Clone(original).(*labpb.LabGroup)
			change(g)
			observation := groupLifecycleObservation(original.Name, g, now)
			if observation.AccessFenced {
				t.Fatal("partial or foreign service certificate fenced access")
			}
			ledger := groupLedgerForCertificate(original)
			ledger.Observe(observation, now)
			if ledger.HeldCompute() == (eventLabModel.Compute{}) {
				t.Fatal("unqualified certificate granted free capacity")
			}
		})
	}
}

func TestGroupServiceReleasePreservesExistingVPNFenceCompatibility(t *testing.T) {
	g, now := stoppedGroupServiceCertificate()
	g.Status.Phase = "Unknown"
	g.Status.Lifecycle.Reason = "OlderCertificate"
	g.Status.Lifecycle.AccessFenced = true
	g.Status.Lifecycle.AccessFencedUnixMs = 1791573544000
	g.Status.Lifecycle.AccessFenceVpnBootId = "original-vpn-boot"
	if !groupLifecycleObservation(g.Name, g, now).AccessFenced {
		t.Fatal("existing complete VPN fence certificate was lost")
	}
}
