package eventLabGroupRepo

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Queries interface {
	ScheduleEventLabGroupRetry(context.Context, postgres.ScheduleEventLabGroupRetryParams) error
	GetEventTeamLabGroup(context.Context, uuid.UUID) (postgres.EventTeamGroupAllocation, error)
	LockEventTeamLabGroup(context.Context, uuid.UUID) (postgres.EventTeamGroupAllocation, error)
	UpdateEventTeamLabGroup(context.Context, postgres.UpdateEventTeamLabGroupParams) (int64, error)
	ListEventLabGroupLifecycleWork(context.Context, postgres.ListEventLabGroupLifecycleWorkParams) ([]postgres.EventTeamGroupAllocation, error)
	LockEventTeamLifecycleLabs(context.Context, uuid.UUID) ([]postgres.EventTeamLab, error)
	GetEventLabPendingStarts(context.Context, uuid.UUID) (int32, error)
}
type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q} }
func (r *Repository) Get(ctx context.Context, teamID uuid.UUID) (eventLabModel.Group, error) {
	row, err := r.q.GetEventTeamLabGroup(ctx, teamID)
	return eventLabAllocationRepo.ToGroupDomain(row), err
}
func (r *Repository) Lock(ctx context.Context, teamID uuid.UUID) (eventLabModel.Group, error) {
	row, err := r.q.LockEventTeamLabGroup(ctx, teamID)
	return eventLabAllocationRepo.ToGroupDomain(row), err
}
func (r *Repository) List(ctx context.Context, now time.Time, limit int32) ([]eventLabModel.Group, error) {
	rows, err := r.q.ListEventLabGroupLifecycleWork(ctx, postgres.ListEventLabGroupLifecycleWorkParams{Now: now, LimitVal: limit})
	out := make([]eventLabModel.Group, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventLabAllocationRepo.ToGroupDomain(row))
	}
	return out, err
}
func (r *Repository) LockChildren(ctx context.Context, teamID uuid.UUID) ([]eventLabModel.Lab, error) {
	rows, err := r.q.LockEventTeamLifecycleLabs(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]eventLabModel.Lab, 0, len(rows))
	for _, row := range rows {
		l, e := eventLabRepo.ToDomain(row)
		if e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, nil
}
func (r *Repository) Pending(ctx context.Context, teamID uuid.UUID) (int32, error) {
	return r.q.GetEventLabPendingStarts(ctx, teamID)
}
func (r *Repository) Update(ctx context.Context, g eventLabModel.Group, expected int64) (bool, error) {
	a, err := json.Marshal(g.Allocation)
	if err != nil {
		return false, err
	}
	ts := func(t *time.Time) pgtype.Timestamptz {
		if t == nil {
			return pgtype.Timestamptz{}
		}
		return pgtype.Timestamptz{Time: *t, Valid: true}
	}
	n, err := r.q.UpdateEventTeamLabGroup(ctx, postgres.UpdateEventTeamLabGroupParams{RetirementStopTarget: targetJSON(g.RetirementStopTarget), RetirementState: state(g.RetirementState), RetirementObservedAt: ts(g.RetirementObservedAt), RetirementError: g.RetirementError, EventTeamID: g.TeamID, AgentUid: g.AgentUID, AgentGeneration: g.AgentGeneration, DesiredRevision: g.Revision, ObservedRevision: g.ObservedRevision, OperationID: g.OperationID, DesiredState: g.DesiredState, ActualState: g.ActualState, Ready: g.Ready, ObservedAt: ts(g.ObservedAt), Allocation: a, AccessFenced: g.AccessFenced, FailureCode: g.FailureCode, FailureMessage: g.FailureMessage, PendingStarts: g.PendingStarts, RetentionUntil: ts(g.RetentionUntil), ProtectedUntil: ts(g.ProtectedUntil), NextAttemptAt: g.NextAttemptAt, UpdatedAt: g.UpdatedAt, ExpectedRevision: expected})
	return n == 1, err
}

func targetJSON(v *eventLabModel.GroupTarget) []byte {
	if v == nil {
		return nil
	}
	raw, _ := json.Marshal(v)
	return raw
}
func state(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}

func (r *Repository) Schedule(ctx context.Context, g eventLabModel.Group, next time.Time) error {
	return r.q.ScheduleEventLabGroupRetry(ctx, postgres.ScheduleEventLabGroupRetryParams{EventTeamID: g.TeamID, OperationID: g.OperationID, DesiredRevision: g.Revision, NextAttemptAt: next})
}
