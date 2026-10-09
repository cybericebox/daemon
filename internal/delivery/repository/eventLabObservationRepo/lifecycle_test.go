package eventLabObservationRepo

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
	"time"
)

func TestGroupProjectionNeedsCurrentNetworkAndPhysicalReleaseProof(t *testing.T) {
	at := time.Now()
	team := uuid.Must(uuid.NewV7())
	event := uuid.Must(uuid.NewV7())
	operation := uuid.Must(uuid.NewV7())
	for _, mode := range []string{"missing current boot", "normal running", "missing release fence"} {
		t.Run(mode, func(t *testing.T) {
			status := &labpb.LabGroupStatus{Phase: "Ready", VpnRegistered: true, CurrentVpnBootAvailable: true, CurrentVpnBootId: "current-boot", CurrentVpnBootObservedUnixMs: at.UnixMilli(), Lifecycle: &labpb.LabLifecycleStatus{LabUid: "group-uid", OperationId: operation.String(), LifecycleRevision: 1, ObservedGeneration: 3, DesiredState: "Running", ObservedState: "Running", Reason: "Normal"}}
			if mode == "missing current boot" {
				status.CurrentVpnBootAvailable = false
			}
			if mode == "missing release fence" {
				status.Lifecycle.DesiredState = "Stopped"
				status.Lifecycle.ObservedState = "Stopped"
				status.Resources = &labpb.ResourceAllocation{OperationId: operation.String(), LifecycleRevision: 1, RuntimeState: "Released", ObservedUnixMs: at.UnixMilli(), ReleasedUnixMs: at.UnixMilli()}
			}
			raw, err := protojson.Marshal(&labpb.MonitoringUpdate{Groups: []*labpb.LabGroup{{Name: "group", Uid: "group-uid", Generation: 3, Status: status}}})
			if err != nil {
				t.Fatal(err)
			}
			q := &currentQueries{rows: map[uuid.UUID]postgres.LabMonitoringCurrent{team: {Payload: raw, ObservedAt: at}}}
			got, err := New(q).Group(context.Background(), event, team, "group")
			if err != nil {
				t.Fatal(err)
			}
			if got.Ready != (mode == "normal running") || got.FailureCode != "" || got.FailureMessage != "" {
				t.Fatalf("readiness/failure %+v", got)
			}
			if mode == "missing release fence" && got.Allocation.RuntimeState == "Released" {
				t.Fatalf("unfenced group release credited %+v", got)
			}
		})
	}
}
