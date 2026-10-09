package event_test

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

func TestACLCleanAcknowledgementRequiresFullFreshCurrentCertificate(t *testing.T) {
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
	if err := uc.ReconcilePendingLabAccess(ctx); err != nil {
		t.Fatal(err)
	}
	var desired, applied int64
	var fingerprint string
	if err := f.db.Pool.QueryRow(ctx, `SELECT desired_revision,applied_revision,policy_fingerprint FROM event_lab_access_syncs WHERE event_team_id=$1`, f.blueID).Scan(&desired, &applied, &fingerprint); err != nil || desired != applied {
		t.Fatalf("fixture not acknowledged %d/%d %v", applied, desired, err)
	}
	cases := []string{"valid", "missing frame", "missing group", "missing policy", "stale monitoring", "future monitoring", "stale boot", "future boot", "missing boot time", "unavailable boot", "changed boot", "group UID", "policy UID", "policy expected group UID", "spec operation", "status operation", "desired revision", "applied revision", "policy generation", "observed generation", "zero generation", "Accepted state", "policy error", "missing applied time", "empty stable fingerprint", "teardown"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			accessMonitoring(t, f, group, "boot-1", &target, "Applied")
			row, err := f.db.Queries.GetLabMonitoringCurrent(ctx, postgres.GetLabMonitoringCurrentParams{EventID: f.eventID, EventTeamID: f.blueID, LabGroupName: group})
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err = json.Unmarshal(row.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			g := payload["groups"].([]any)[0].(map[string]any)
			gs := g["status"].(map[string]any)
			p := payload["policies"].([]any)[0].(map[string]any)
			ps := p["status"].(map[string]any)
			// Current frame is one second old, avoiding sub-millisecond host/DB
			// clock precision races while preserving the strict future rejection.
			at := time.Now().Add(-time.Second)
			gs["currentVpnBootObservedUnixMs"] = at.UnixMilli()
			ps["appliedAtUnixMs"] = at.UnixMilli()
			switch name {
			case "missing group":
				payload["groups"] = []any{}
			case "missing policy":
				payload["policies"] = []any{}
			case "stale monitoring":
				at = at.Add(-time.Minute)
			case "future monitoring":
				at = at.Add(time.Minute)
			case "stale boot":
				gs["currentVpnBootObservedUnixMs"] = time.Now().Add(-time.Minute).UnixMilli()
			case "future boot":
				gs["currentVpnBootObservedUnixMs"] = time.Now().Add(time.Minute).UnixMilli()
			case "missing boot time":
				delete(gs, "currentVpnBootObservedUnixMs")
			case "unavailable boot":
				gs["currentVpnBootAvailable"] = false
			case "changed boot":
				gs["currentVpnBootId"] = "boot-2"
			case "group UID":
				g["uid"] = "recreated-group"
			case "policy UID":
				delete(p, "policyUid")
			case "policy expected group UID":
				p["expectedGroupUid"] = "other-group"
			case "spec operation":
				p["operationId"] = uuid.Must(uuid.NewV7()).String()
			case "status operation":
				ps["operationId"] = uuid.Must(uuid.NewV7()).String()
			case "desired revision":
				p["desiredRevision"] = target.Revision + 1
			case "applied revision":
				ps["appliedRevision"] = target.Revision - 1
			case "policy generation":
				p["generation"] = "4"
			case "observed generation":
				ps["observedGeneration"] = "2"
			case "zero generation":
				p["generation"] = "0"
				ps["observedGeneration"] = "0"
			case "Accepted state":
				ps["state"] = "Accepted"
			case "policy error":
				ps["lastError"] = "conntrack failed"
			case "missing applied time":
				delete(ps, "appliedAtUnixMs")
			case "empty stable fingerprint":
				if _, err = f.db.Pool.Exec(ctx, `UPDATE event_lab_access_syncs SET policy_fingerprint='' WHERE event_team_id=$1`, f.blueID); err != nil {
					t.Fatal(err)
				}
			case "teardown":
				if _, err = f.db.Pool.Exec(ctx, `UPDATE event_stand_rollouts SET torn_down_at=now() WHERE event_id=$1`, f.eventID); err != nil {
					t.Fatal(err)
				}
				payload["groups"] = []any{}
				payload["policies"] = []any{}
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if name == "missing frame" {
				_, err = f.db.Pool.Exec(ctx, `DELETE FROM lab_monitoring_current WHERE event_team_id=$1`, f.blueID)
			} else {
				err = f.db.Queries.UpsertLabMonitoringCurrent(ctx, postgres.UpsertLabMonitoringCurrentParams{EventID: f.eventID, EventTeamID: f.blueID, LabGroupName: group, AgentID: "agent", Sequence: 2, ObservedAt: at, UpdatedAt: at, Payload: raw})
			}
			if err != nil {
				t.Fatal(err)
			}
			dirty, err := f.db.Queries.ListDirtyEventLabAccessSyncs(ctx, 100)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, sync := range dirty {
				if sync.EventTeamID == f.blueID {
					found = true
				}
			}
			wantDirty := name != "valid" && name != "teardown"
			if found != wantDirty {
				t.Errorf("clean ACK certificate %s: dirty=%t want=%t", name, found, wantDirty)
			}
			// The probe must not mutate the historical applied revision or target.
			var afterDesired, afterApplied int64
			if err = f.db.Pool.QueryRow(ctx, `SELECT desired_revision,applied_revision FROM event_lab_access_syncs WHERE event_team_id=$1`, f.blueID).Scan(&afterDesired, &afterApplied); err != nil || afterDesired != desired || afterApplied != applied {
				t.Fatal(afterDesired, afterApplied, err)
			}
			if name == "empty stable fingerprint" {
				if _, err = f.db.Pool.Exec(ctx, `UPDATE event_lab_access_syncs SET policy_fingerprint=$2 WHERE event_team_id=$1`, f.blueID, fingerprint); err != nil {
					t.Fatal(err)
				}
			}
			if name == "teardown" {
				if _, err = f.db.Pool.Exec(ctx, `UPDATE event_stand_rollouts SET torn_down_at=NULL WHERE event_id=$1`, f.eventID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
