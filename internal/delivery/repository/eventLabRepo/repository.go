// Package eventLabRepo persists whole lifecycle aggregates and immutable objective pins.
// RecordObservation and RecordInitialIdentity are conditional narrow-write exceptions:
// the former never changes desired intent, the latter only adopts a first deployment UID.
package eventLabRepo

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Queries interface {
	ListPendingStoppedEventTeamLabs(context.Context, postgres.ListPendingStoppedEventTeamLabsParams) ([]postgres.EventTeamLab, error)
	ScheduleEventTeamLabLifecycleRetry(context.Context, postgres.ScheduleEventTeamLabLifecycleRetryParams) (int64, error)
	AttachEventLabAssignmentBindings(ctx context.Context, labID uuid.NullUUID) (int64, error)
	CreateEventTeamLab(ctx context.Context, arg postgres.CreateEventTeamLabParams) error
	GetEventTeamLab(ctx context.Context, id uuid.UUID) (postgres.EventTeamLab, error)
	GetEventTeamLabForChallenge(ctx context.Context, arg postgres.GetEventTeamLabForChallengeParams) (postgres.EventTeamLab, error)
	GetEventTeamLabForRef(ctx context.Context, arg postgres.GetEventTeamLabForRefParams) (postgres.EventTeamLab, error)
	IsEventTeamLabComplete(ctx context.Context, id uuid.UUID) (pgtype.Bool, error)
	ListDirtyEventTeamLabs(ctx context.Context, arg postgres.ListDirtyEventTeamLabsParams) ([]postgres.EventTeamLab, error)
	ListEventLabAssignmentObjectives(ctx context.Context, arg postgres.ListEventLabAssignmentObjectivesParams) ([]postgres.ListEventLabAssignmentObjectivesRow, error)
	ListEventLabAssignmentsMissingIdentity(ctx context.Context, eventID uuid.UUID) ([]postgres.ListEventLabAssignmentsMissingIdentityRow, error)
	LockEventTeamForLabAdmission(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	LockEventTeamLab(ctx context.Context, id uuid.UUID) (postgres.EventTeamLab, error)
	RecordEventTeamLabInitialIdentity(ctx context.Context, arg postgres.RecordEventTeamLabInitialIdentityParams) (int64, error)
	RecordEventTeamLabObservation(ctx context.Context, arg postgres.RecordEventTeamLabObservationParams) (int64, error)
	UpdateEventTeamLab(ctx context.Context, arg postgres.UpdateEventTeamLabParams) (int64, error)
}
type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (eventLabModel.Lab, error) {
	row, err := r.q.GetEventTeamLab(ctx, id)
	if err != nil {
		return eventLabModel.Lab{}, err
	}
	return ToDomain(row)
}
func (r *Repository) GetForChallenge(ctx context.Context, team, challenge uuid.UUID) (eventLabModel.Lab, error) {
	row, err := r.q.GetEventTeamLabForChallenge(ctx, postgres.GetEventTeamLabForChallengeParams{EventTeamID: team, EventChallengeID: challenge})
	if err != nil {
		return eventLabModel.Lab{}, err
	}
	return ToDomain(row)
}
func (r *Repository) GetForRef(ctx context.Context, team uuid.UUID, ref eventLabModel.Ref, generation int32) (eventLabModel.Lab, error) {
	row, err := r.q.GetEventTeamLabForRef(ctx, postgres.GetEventTeamLabForRefParams{EventTeamID: team, LabGroupName: ref.Group, LabName: ref.Lab, Generation: generation})
	if err != nil {
		return eventLabModel.Lab{}, err
	}
	return ToDomain(row)
}
func (r *Repository) Lock(ctx context.Context, id uuid.UUID) (eventLabModel.Lab, error) {
	row, err := r.q.LockEventTeamLab(ctx, id)
	if err != nil {
		return eventLabModel.Lab{}, err
	}
	return ToDomain(row)
}
func (r *Repository) Create(ctx context.Context, l eventLabModel.Lab, ids []uuid.UUID) error {
	if int32(len(ids)) != l.ObjectiveCount || len(ids) == 0 {
		return fmt.Errorf("objective membership does not match lab count")
	}
	allocation, err := json.Marshal(l.Allocation)
	if err != nil {
		return err
	}
	return r.q.CreateEventTeamLab(ctx, postgres.CreateEventTeamLabParams{
		ObjectiveIds:         ids,
		ID:                   l.ID,
		EventID:              l.EventID,
		EventTeamID:          l.TeamID,
		EventExerciseID:      l.EventExerciseID,
		VariantIndex:         l.VariantIndex,
		Generation:           l.Generation,
		LabGroupName:         l.Ref.Group,
		LabName:              l.Ref.Lab,
		ObjectiveCount:       l.ObjectiveCount,
		CreatedAt:            l.CreatedAt,
		AgentUid:             l.AgentUID,
		AgentGeneration:      l.AgentGeneration,
		DesiredRevision:      l.Revision,
		ObservedRevision:     l.ObservedRevision,
		OperationID:          l.OperationID,
		DesiredState:         l.DesiredState,
		ActualState:          l.ActualState,
		RuntimeReady:         l.RuntimeReady,
		CloseReason:          text(l.CloseReason),
		LogicalClosedAt:      timestamp(l.ClosedAt),
		SnapshotMode:         l.SnapshotMode,
		SnapshotState:        l.SnapshotState,
		RetentionUntil:       timestamp(l.RetentionUntil),
		ProtectedUntil:       timestamp(l.ProtectedUntil),
		ActualStoppedAt:      timestamp(l.ActualStoppedAt),
		ObservedAt:           timestamp(l.ObservedAt),
		Materialized:         l.Materialized,
		Allocation:           allocation,
		FailureCode:          l.FailureCode,
		FailureMessage:       l.FailureMessage,
		AccessFenced:         l.AccessFenced,
		AccessFencedAt:       timestamp(l.AccessFencedAt),
		AccessFenceVpnBootID: l.AccessFenceVPNBootID,
		NextAttemptAt:        l.NextAttemptAt,
		UpdatedAt:            l.UpdatedAt,
	})
}
func (r *Repository) Update(ctx context.Context, l eventLabModel.Lab, expectedRevision int64) (bool, error) {
	allocation, err := json.Marshal(l.Allocation)
	if err != nil {
		return false, err
	}
	n, err := r.q.UpdateEventTeamLab(ctx, postgres.UpdateEventTeamLabParams{
		AgentUid:             l.AgentUID,
		AgentGeneration:      l.AgentGeneration,
		DesiredRevision:      l.Revision,
		ObservedRevision:     l.ObservedRevision,
		OperationID:          l.OperationID,
		DesiredState:         l.DesiredState,
		ActualState:          l.ActualState,
		RuntimeReady:         l.RuntimeReady,
		CloseReason:          text(l.CloseReason),
		LogicalClosedAt:      timestamp(l.ClosedAt),
		SnapshotMode:         l.SnapshotMode,
		SnapshotState:        l.SnapshotState,
		RetentionUntil:       timestamp(l.RetentionUntil),
		ProtectedUntil:       timestamp(l.ProtectedUntil),
		ActualStoppedAt:      timestamp(l.ActualStoppedAt),
		ObservedAt:           timestamp(l.ObservedAt),
		Materialized:         l.Materialized,
		Allocation:           allocation,
		FailureCode:          l.FailureCode,
		FailureMessage:       l.FailureMessage,
		AccessFenced:         l.AccessFenced,
		AccessFencedAt:       timestamp(l.AccessFencedAt),
		AccessFenceVpnBootID: l.AccessFenceVPNBootID,
		NextAttemptAt:        l.NextAttemptAt,
		UpdatedAt:            l.UpdatedAt,
		ID:                   l.ID,
		ExpectedRevision:     expectedRevision,
	})
	return n == 1, err
}
func (r *Repository) Complete(ctx context.Context, id uuid.UUID) (bool, error) {
	complete, err := r.q.IsEventTeamLabComplete(ctx, id)
	return complete.Valid && complete.Bool, err
}
func (r *Repository) ListDirty(ctx context.Context, now time.Time, limit int32) ([]eventLabModel.Lab, error) {
	rows, err := r.q.ListDirtyEventTeamLabs(ctx, postgres.ListDirtyEventTeamLabsParams{Now: now, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]eventLabModel.Lab, 0, len(rows))
	for _, row := range rows {
		l, err := ToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}
func (r *Repository) RecordInitialIdentity(ctx context.Context, id uuid.UUID, ref eventLabModel.Ref, uid string, generation int64, now time.Time) (bool, error) {
	l, err := r.Get(ctx, id)
	if err != nil {
		return false, err
	}
	n, err := r.q.RecordEventTeamLabInitialIdentity(ctx, postgres.RecordEventTeamLabInitialIdentityParams{ID: id, LabGroupName: ref.Group, LabName: ref.Lab, AgentUid: uid, AgentGeneration: generation, Generation: l.Generation, Now: now})
	return n == 1, err
}
func (r *Repository) RecordObservation(ctx context.Context, id uuid.UUID, o eventLabModel.Observation) (bool, error) {
	if o.ObservedAt == nil {
		return false, nil
	}
	l, err := r.Get(ctx, id)
	if err != nil {
		return false, err
	}
	expectedUpdatedAt, expectedObservedAt := l.UpdatedAt, l.ObservedAt
	// Use the domain's single conservative allocation rule. The SQL write is
	// conditional on the loaded row as well as the lifecycle identity, so a
	// concurrent observation cannot apply a ledger normalized from stale data.
	if !l.Observe(o, *o.ObservedAt) {
		return false, nil
	}
	allocation, err := json.Marshal(l.Allocation)
	if err != nil {
		return false, err
	}
	n, err := r.q.RecordEventTeamLabObservation(ctx, postgres.RecordEventTeamLabObservationParams{ID: id, LabGroupName: o.Ref.Group, LabName: o.Ref.Lab, AgentUid: o.UID, AgentGeneration: o.Generation, ObservedGeneration: o.ObservedGeneration, OperationID: o.OperationID, DesiredRevision: o.Revision, DesiredState: o.DesiredState, ActualState: l.ActualState, SnapshotState: l.SnapshotState, ActualStoppedAt: timestamp(l.ActualStoppedAt), ObservedAt: timestamp(l.ObservedAt), RuntimeReady: l.RuntimeReady, Allocation: allocation, FailureCode: l.FailureCode, FailureMessage: l.FailureMessage, AccessFenced: l.AccessFenced, AccessFencedAt: timestamp(l.AccessFencedAt), AccessFenceVpnBootID: l.AccessFenceVPNBootID, ExpectedUpdatedAt: expectedUpdatedAt, ExpectedObservedAt: timestamp(expectedObservedAt)})
	return n == 1, err
}

func (r *Repository) AttachBindings(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.AttachEventLabAssignmentBindings(ctx, uuid.NullUUID{UUID: id, Valid: true})
}
func ToDomain(row postgres.EventTeamLab) (eventLabModel.Lab, error) {
	allocation := eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}
	if err := json.Unmarshal(row.Allocation, &allocation); err != nil {
		// Historical JSON shape corruption is not a read-side invariant failure.
		// Conservatively keep every resource unknown, never credit a partial decode.
		allocation = eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}
	}
	if allocation.RuntimeState == "" {
		allocation.RuntimeState = "Unknown"
	}
	if allocation.StorageState == "" {
		allocation.StorageState = "Unknown"
	}
	return eventLabModel.Lab{ID: row.ID, EventID: row.EventID, TeamID: row.EventTeamID, EventExerciseID: row.EventExerciseID, Ref: eventLabModel.Ref{Group: row.LabGroupName, Lab: row.LabName}, VariantIndex: row.VariantIndex, Generation: row.Generation, AgentUID: row.AgentUid, AgentGeneration: row.AgentGeneration, Revision: row.DesiredRevision, ObservedRevision: row.ObservedRevision, OperationID: row.OperationID, DesiredState: row.DesiredState, ActualState: row.ActualState, CloseReason: row.CloseReason.String, SnapshotMode: row.SnapshotMode, SnapshotState: row.SnapshotState, ClosedAt: timePtr(row.LogicalClosedAt), RetentionUntil: timePtr(row.RetentionUntil), ProtectedUntil: timePtr(row.ProtectedUntil), ActualStoppedAt: timePtr(row.ActualStoppedAt), ObservedAt: timePtr(row.ObservedAt), ObjectiveCount: row.ObjectiveCount, Materialized: row.Materialized, RuntimeReady: row.RuntimeReady, Allocation: allocation, FailureCode: row.FailureCode, FailureMessage: row.FailureMessage, AccessFenced: row.AccessFenced, AccessFencedAt: timePtr(row.AccessFencedAt), AccessFenceVPNBootID: row.AccessFenceVpnBootID, NextAttemptAt: row.NextAttemptAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}
func timestamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	copy := t.Time
	return &copy
}
func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// AssignmentObjective is the pinned question with its optional deployment binding.
type AssignmentObjective struct {
	ID               uuid.UUID
	VariantIndex     int32
	BindingID, LabID uuid.NullUUID
	Ref              eventLabModel.Ref
	Generation       int32
}
type Assignment struct{ TeamID, EventExerciseID uuid.UUID }

func (r *Repository) AssignmentObjectives(ctx context.Context, eventID, teamID, setID uuid.UUID) ([]AssignmentObjective, error) {
	rows, err := r.q.ListEventLabAssignmentObjectives(ctx, postgres.ListEventLabAssignmentObjectivesParams{EventID: eventID, EventTeamID: teamID, EventExerciseID: setID})
	if err != nil {
		return nil, err
	}
	out := make([]AssignmentObjective, 0, len(rows))
	for _, row := range rows {
		out = append(out, AssignmentObjective{ID: row.EventChallengeID, VariantIndex: row.VariantIndex, BindingID: row.BindingID, LabID: row.LabID, Ref: eventLabModel.Ref{Group: row.LabGroupName.String, Lab: row.LabName.String}, Generation: row.Generation.Int32})
	}
	return out, nil
}
func (r *Repository) MissingAssignments(ctx context.Context, eventID uuid.UUID) ([]Assignment, error) {
	rows, err := r.q.ListEventLabAssignmentsMissingIdentity(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		out = append(out, Assignment{TeamID: row.EventTeamID, EventExerciseID: row.EventExerciseID})
	}
	return out, nil
}
func (r *Repository) LockAdmission(ctx context.Context, teamID uuid.UUID) error {
	_, err := r.q.LockEventTeamForLabAdmission(ctx, teamID)
	return err
}

func (r *Repository) PendingStopped(ctx context.Context, now time.Time, limit int32) ([]eventLabModel.Lab, error) {
	rows, err := r.q.ListPendingStoppedEventTeamLabs(ctx, postgres.ListPendingStoppedEventTeamLabsParams{Now: now, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]eventLabModel.Lab, 0, len(rows))
	for _, row := range rows {
		l, err := ToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}
func (r *Repository) ScheduleLifecycleRetry(ctx context.Context, l eventLabModel.Lab, now, next time.Time) error {
	_, err := r.q.ScheduleEventTeamLabLifecycleRetry(ctx, postgres.ScheduleEventTeamLabLifecycleRetryParams{ID: l.ID, DesiredRevision: l.Revision, OperationID: l.OperationID, Now: now, NextAttemptAt: next})
	return err
}
