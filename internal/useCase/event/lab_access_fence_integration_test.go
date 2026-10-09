package event_test

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

type revisionAccessAgent struct {
	*standAgent
	targets  []eventLabModel.AccessTarget
	policies [][]labAccessModel.ClientPolicy
}

func (a *revisionAccessAgent) SetLabGroupVPNDisabled(context.Context, string, bool) error { return nil }
func (a *revisionAccessAgent) SetLabGroupSuspended(context.Context, string, bool) error   { return nil }
func (a *revisionAccessAgent) ReconcileLabGroupAccess(context.Context, string, []labAccessModel.ClientPolicy) error {
	return nil
}
func (a *revisionAccessAgent) ReconcileLabGroupAccessRevision(_ context.Context, _ string, p []labAccessModel.ClientPolicy, target eventLabModel.AccessTarget) error {
	a.targets = append(a.targets, target)
	a.policies = append(a.policies, p)
	return nil
}

func accessMonitoring(t *testing.T, f *standFixture, group, boot string, target *eventLabModel.AccessTarget, state string) {
	t.Helper()
	now := time.Now()
	payload := map[string]any{"groups": []any{map[string]any{"name": group, "uid": "group-uid", "generation": "1", "status": map[string]any{"currentVpnBootId": boot, "currentVpnBootAvailable": true, "currentVpnBootObservedUnixMs": now.UnixMilli()}}}}
	if target != nil {
		payload["policies"] = []any{map[string]any{"labGroupName": group, "expectedGroupUid": "group-uid", "policyUid": "policy-uid", "generation": "3", "operationId": target.OperationID.String(), "desiredRevision": target.Revision, "status": map[string]any{"operationId": target.OperationID.String(), "appliedRevision": target.Revision, "observedGeneration": "3", "state": state, "vpnBootId": "boot-1", "appliedAtUnixMs": now.UnixMilli()}}}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.Queries.UpsertLabMonitoringCurrent(context.Background(), postgres.UpsertLabMonitoringCurrentParams{EventID: f.eventID, EventTeamID: f.blueID, LabGroupName: group, AgentID: "agent", Sequence: 1, ObservedAt: now, UpdatedAt: now, Payload: raw}); err != nil {
		t.Fatal(err)
	}
}
func TestACLAcceptedIsNotPhysicalFence(t *testing.T) {
	f, _, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	group := testLabGroup(f.eventID, f.blueID)
	agent := &revisionAccessAgent{standAgent: f.agent}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: agent, InfrastructureCapability: standCapability{}})
	accessMonitoring(t, f, group, "boot-1", nil, "")
	if err := uc.RequestLabAccessSync(ctx, f.blueID); err != nil {
		t.Fatal(err)
	}
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	var desired, applied int64
	if err := f.db.Pool.QueryRow(ctx, `SELECT desired_revision,applied_revision FROM event_lab_access_syncs WHERE event_team_id=$1`, f.blueID).Scan(&desired, &applied); err != nil {
		t.Fatal(err)
	}
	if applied != 0 || len(agent.targets) != 1 {
		t.Fatalf("accepted falsely acknowledged: %d/%d targets=%v", applied, desired, agent.targets)
	}
	first := agent.targets[0]
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if len(agent.targets) != 2 || agent.targets[1] != first {
		t.Fatalf("retry changed identity: %+v", agent.targets)
	}
	accessMonitoring(t, f, group, "boot-1", &first, "Applied")
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT desired_revision,applied_revision FROM event_lab_access_syncs WHERE event_team_id=$1`, f.blueID).Scan(&desired, &applied); err != nil || applied != desired {
		t.Fatalf("physical ack %d/%d %v", applied, desired, err)
	}
	accessMonitoring(t, f, group, "boot-2", &first, "Applied")
	dirty, err := f.db.Queries.ListDirtyEventLabAccessSyncs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range dirty {
		if row.EventTeamID == f.blueID {
			found = true
		}
	}
	if !found {
		t.Fatal("VPN restart did not invalidate fence")
	}
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	// Replaying the same revision to a new VPN boot does not turn old status into proof.
	var ackBoot string
	if err = f.db.Pool.QueryRow(ctx, `SELECT access_fence_vpn_boot_id FROM event_lab_access_syncs WHERE event_team_id=$1`, f.blueID).Scan(&ackBoot); err != nil || ackBoot != "boot-1" {
		t.Fatalf("stale ack accepted %q %v", ackBoot, err)
	}
}

func TestACLBoundaryGetsNewRevisionOnce(t *testing.T) {
	f, _, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	group := testLabGroup(f.eventID, f.blueID)
	agent := &revisionAccessAgent{standAgent: f.agent}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: agent, InfrastructureCapability: standCapability{}})
	accessMonitoring(t, f, group, "boot-1", nil, "")
	if err := uc.RequestLabAccessSync(ctx, f.blueID); err != nil {
		t.Fatal(err)
	}
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if len(agent.targets) == 0 {
		t.Fatal("revision writer was not called")
	}
	first := agent.targets[0]
	// No Request call: the event boundary changes the complete normalized policy.
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET manual_finished_at=now()-interval '1 second' WHERE id=$1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	second := agent.targets[len(agent.targets)-1]
	if second.Revision != first.Revision+1 || second.OperationID == first.OperationID || second.OperationID == uuid.Nil {
		t.Fatalf("boundary reused identity: %+v -> %+v", first, second)
	}
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if agent.targets[len(agent.targets)-1] != second {
		t.Fatal("boundary retry incremented again")
	}
}

func TestACLVPNRestartBetweenObservationAndAckDenied(t *testing.T) {
	f, _, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	group := testLabGroup(f.eventID, f.blueID)
	agent := &revisionAccessAgent{standAgent: f.agent}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: agent, InfrastructureCapability: standCapability{}})
	accessMonitoring(t, f, group, "boot-1", nil, "")
	if err := uc.RequestLabAccessSync(ctx, f.blueID); err != nil {
		t.Fatal(err)
	}
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	target := agent.targets[0]
	accessMonitoring(t, f, group, "boot-1", &target, "Applied")
	repo := labAccessSyncRepo.New(f.db.Queries)
	rows, err := repo.ListDirty(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sync labAccessSyncRepo.Sync
	for _, row := range rows {
		if row.TeamID == f.blueID {
			sync = row
		}
	}
	got, err := repo.ObserveAccessFence(ctx, f.blueID)
	if err != nil {
		t.Fatal(err)
	}
	accessMonitoring(t, f, group, "boot-2", &target, "Applied")
	changed, err := repo.MarkApplied(ctx, sync, target, sync.PolicyFingerprint, got, time.Now())
	if err != nil || changed != 0 {
		t.Fatalf("stale observed boot acknowledged: %d %v", changed, err)
	}
}
