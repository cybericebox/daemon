package labAccessSyncRepo

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

func (r *Repository) Materialize(ctx context.Context, sync Sync, fingerprint, groupUID string, now time.Time) (eventLabModel.AccessTarget, bool, error) {
	target := eventLabModel.AccessTarget{ExpectedGroupUID: groupUID, OperationID: sync.OperationID, Revision: sync.DesiredRevision}
	if sync.PolicyFingerprint == fingerprint && sync.ExpectedGroupUID == groupUID && sync.OperationID != uuid.Nil {
		return target, true, nil
	}
	row, err := r.q.MaterializeEventLabAccessPolicy(ctx, postgres.MaterializeEventLabAccessPolicyParams{EventTeamID: sync.TeamID, ExpectedRevision: sync.DesiredRevision, OperationID: uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}, PolicyFingerprint: fingerprint, ExpectedGroupUid: groupUID, UpdatedAt: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return target, false, nil
	}
	if err != nil {
		return target, false, err
	}
	target.OperationID = row.OperationID.UUID
	target.Revision = row.DesiredRevision
	return target, true, nil
}

// ObserveAccessFence reads the merged current Monitoring payload, rather than
// treating RPC acceptance or an invented ListPolicy RPC as physical proof.
func (r *Repository) ObserveAccessFence(ctx context.Context, teamID uuid.UUID) (eventLabModel.AccessFenceObservation, error) {
	out := eventLabModel.AccessFenceObservation{}
	row, err := r.q.GetCurrentEventLabAccessMonitoring(ctx, teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	update := &labpb.MonitoringUpdate{}
	if err = protojson.Unmarshal(row.Payload, update); err != nil {
		return out, err
	}
	out.ObservedAt = row.ObservedAt
	out.Group = row.LabGroupName
	for _, group := range update.GetGroups() {
		if group.GetName() == out.Group {
			out.ExpectedGroupUID = group.GetUid()
		}
	}
	// Current boot is an independently validated producer startup tuple: live
	// group UID, Pod UID and container/restart incarnation. Policy status is
	// only the comparison operand, never the source of currentness.
	for _, group := range update.GetGroups() {
		if group.GetName() == out.Group {
			status := group.GetStatus()
			if status.GetCurrentVpnBootAvailable() {
				out.CurrentVPNBootID = status.GetCurrentVpnBootId()
				out.VPNObservedAt = time.UnixMilli(status.GetCurrentVpnBootObservedUnixMs())
			}
		}
	}
	for _, policy := range update.GetPolicies() {
		if policy.GetLabGroupName() != out.Group {
			continue
		}
		if policy.GetExpectedGroupUid() != out.ExpectedGroupUID {
			return out, nil
		}
		s := policy.GetStatus()
		out.PolicyUID = policy.GetPolicyUid()
		out.DesiredRevision = policy.GetDesiredRevision()
		out.AppliedRevision = s.GetAppliedRevision()
		out.Generation = policy.GetGeneration()
		out.ObservedGeneration = s.GetObservedGeneration()
		out.State = s.GetState()
		out.OperationID = uuid.FromStringOrNil(s.GetOperationId())
		out.VPNBootID = s.GetVpnBootId()
		out.LastError = s.GetLastError()
		if s.GetAppliedAtUnixMs() <= 0 || policy.GetOperationId() != s.GetOperationId() {
			out.State = "Unknown"
		}
		return out, nil
	}
	return out, nil
}

func (r *Repository) MarkApplied(ctx context.Context, sync Sync, target eventLabModel.AccessTarget, fingerprint string, got eventLabModel.AccessFenceObservation, now time.Time) (int64, error) {
	age := now.Sub(got.ObservedAt)
	vpnAge := now.Sub(got.VPNObservedAt)
	if age < 0 || age > 30*time.Second || vpnAge < 0 || vpnAge > 30*time.Second || !eventLabModel.AccessFenceMatches(target, got, got.CurrentVPNBootID) {
		return 0, nil
	}
	return r.q.MarkEventLabAccessSyncApplied(ctx, postgres.MarkEventLabAccessSyncAppliedParams{LabGroupName: target.Group, EventTeamID: sync.TeamID, DesiredRevision: target.Revision, RuntimeOpen: sync.RuntimeOpen, VpnEnabled: sync.VPNEnabled, StageEpoch: sync.StageEpoch, UpdatedAt: now, OperationID: target.OperationID, PolicyFingerprint: fingerprint, ExpectedGroupUid: target.ExpectedGroupUID, AccessFenceVpnBootID: got.CurrentVPNBootID})
}
