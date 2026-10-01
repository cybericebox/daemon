// Package labBindingRepo maps durable Laboratory handles to the event domain.
package labBindingRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

type Queries interface {
	GetLabBinding(ctx context.Context, arg postgres.GetLabBindingParams) (postgres.LabBinding, error)
	CreateLabBinding(ctx context.Context, arg postgres.CreateLabBindingParams) (postgres.LabBinding, error)
	UpdateLabBindingReadiness(ctx context.Context, arg postgres.UpdateLabBindingReadinessParams) (int64, error)
	ListWithdrawnLabBindings(ctx context.Context, now pgtype.Timestamptz) ([]postgres.LabBinding, error)
	MarkLabBindingDestroyed(ctx context.Context, id uuid.UUID) (int64, error)
	QueueTeamLabGroupCleanup(ctx context.Context, arg postgres.QueueTeamLabGroupCleanupParams) (int64, error)
	QueueWithdrawnEmptyLabGroups(ctx context.Context, now time.Time) error
	ListPendingLabGroupCleanupRequests(ctx context.Context) ([]string, error)
	MarkLabGroupCleanupRequestDestroyed(ctx context.Context, arg postgres.MarkLabGroupCleanupRequestDestroyedParams) (int64, error)
	ListPendingEventLabBindings(ctx context.Context, eventID uuid.UUID) ([]postgres.ListPendingEventLabBindingsRow, error)
	MarkLabBindingDeployed(ctx context.Context, arg postgres.MarkLabBindingDeployedParams) (int64, error)
	MarkLabBindingFailedWithReason(ctx context.Context, arg postgres.MarkLabBindingFailedWithReasonParams) (int64, error)
	MarkStandLabReady(ctx context.Context, arg postgres.MarkStandLabReadyParams) (int64, error)
	ListTeamInfrastructureLabs(ctx context.Context, eventTeamID uuid.UUID) ([]postgres.LabBinding, error)
	RecreateLabBinding(ctx context.Context, arg postgres.RecreateLabBindingParams) (int64, error)
	ResetUnpublishedTeamChallengesForRecreate(ctx context.Context, eventTeamID uuid.UUID) error
	MarkEventLabBindingsDestroyed(ctx context.Context, eventID uuid.UUID) error
}

// PendingLab is one not yet ready Lab with what its deploy needs: the pinned
// exercise version and the team's variant.
type PendingLab struct {
	Binding           labBindingModel.Binding
	VariantIndex      int32
	ExerciseVersionID uuid.UUID
}

func (r *Repository) UpdateReadiness(ctx context.Context, id uuid.UUID, expected, next labBindingModel.Readiness) (int64, error) {
	return r.q.UpdateLabBindingReadiness(ctx, postgres.UpdateLabBindingReadinessParams{ID: id, ExpectedReadiness: int16(expected), Readiness: int16(next)})
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Get(ctx context.Context, teamID, eventChallengeID uuid.UUID) (labBindingModel.Binding, error) {
	row, err := r.q.GetLabBinding(ctx, postgres.GetLabBindingParams{EventTeamID: teamID, EventChallengeID: eventChallengeID})
	if err != nil {
		return labBindingModel.Binding{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) ListWithdrawn(ctx context.Context, now time.Time) ([]labBindingModel.Binding, error) {
	rows, err := r.q.ListWithdrawnLabBindings(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil {
		return nil, err
	}
	items := make([]labBindingModel.Binding, 0, len(rows))
	for _, row := range rows {
		items = append(items, ToDomain(row))
	}
	return items, nil
}

func (r *Repository) MarkDestroyed(ctx context.Context, id uuid.UUID) (int64, error) {
	return r.q.MarkLabBindingDestroyed(ctx, id)
}

func (r *Repository) QueueTeamCleanup(ctx context.Context, teamID uuid.UUID, requestedAt time.Time) (int64, error) {
	return r.q.QueueTeamLabGroupCleanup(ctx, postgres.QueueTeamLabGroupCleanupParams{
		EventTeamID: teamID,
		RequestedAt: requestedAt,
	})
}

func (r *Repository) QueueWithdrawnEmptyGroups(ctx context.Context, now time.Time) error {
	return r.q.QueueWithdrawnEmptyLabGroups(ctx, now)
}

func (r *Repository) ListPendingCleanupRequests(ctx context.Context) ([]string, error) {
	return r.q.ListPendingLabGroupCleanupRequests(ctx)
}

func (r *Repository) MarkCleanupRequestDestroyed(ctx context.Context, groupName string, destroyedAt time.Time) (int64, error) {
	return r.q.MarkLabGroupCleanupRequestDestroyed(ctx, postgres.MarkLabGroupCleanupRequestDestroyedParams{
		LabGroupName: groupName,
		DestroyedAt:  pgtype.Timestamptz{Time: destroyedAt, Valid: true},
	})
}

// Create inserts a binding. created is false when another request already
// created the binding for this team and event exercise.
func (r *Repository) Create(ctx context.Context, value labBindingModel.Binding) (labBindingModel.Binding, bool, error) {
	row, err := r.q.CreateLabBinding(ctx, postgres.CreateLabBindingParams{ID: value.ID, EventID: value.EventID, EventTeamID: value.EventTeamID, EventChallengeID: value.EventChallengeID, LabGroupName: value.LabGroupName, LabName: value.LabName, CreatedAt: value.CreatedAt, Generation: value.Generation})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return labBindingModel.Binding{}, false, nil
		}
		return labBindingModel.Binding{}, false, err
	}
	return ToDomain(row), true, nil
}

// ListPending returns every pending Lab of an event, oldest first.
func (r *Repository) ListPending(ctx context.Context, eventID uuid.UUID) ([]PendingLab, error) {
	rows, err := r.q.ListPendingEventLabBindings(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]PendingLab, 0, len(rows))
	for _, row := range rows {
		binding := labBindingModel.Binding{ID: row.ID, EventID: eventID, EventTeamID: row.EventTeamID, EventChallengeID: row.EventChallengeID,
			LabGroupName: row.LabGroupName, LabName: row.LabName, Generation: row.Generation, CreatedAt: row.CreatedAt,
			Readiness: labBindingModel.ReadinessPending}
		if row.DeployedAt.Valid {
			at := row.DeployedAt.Time
			binding.DeployedAt = &at
		}
		out = append(out, PendingLab{Binding: binding, VariantIndex: row.VariantIndex, ExerciseVersionID: row.ExerciseVersionID})
	}
	return out, nil
}

// MarkDeployed records that the agent accepted this generation's Lab.
func (r *Repository) MarkDeployed(ctx context.Context, value labBindingModel.Binding, at time.Time) (bool, error) {
	affected, err := r.q.MarkLabBindingDeployed(ctx, postgres.MarkLabBindingDeployedParams{ID: value.ID, Generation: value.Generation, DeployedAt: pgtype.Timestamptz{Time: at, Valid: true}})
	return affected == 1, err
}

// MarkFailed fails this generation's pending Lab with a reason.
func (r *Repository) MarkFailed(ctx context.Context, value labBindingModel.Binding, reason string) (bool, error) {
	affected, err := r.q.MarkLabBindingFailedWithReason(ctx, postgres.MarkLabBindingFailedWithReasonParams{ID: value.ID, Generation: value.Generation, FailureReason: pgtype.Text{String: reason, Valid: true}})
	return affected == 1, err
}

// MarkReady marks this generation's Lab ready and its preparing team
// challenge ready in one statement.
func (r *Repository) MarkReady(ctx context.Context, value labBindingModel.Binding) (bool, error) {
	affected, err := r.q.MarkStandLabReady(ctx, postgres.MarkStandLabReadyParams{ID: value.ID, Generation: value.Generation})
	return affected == 1, err
}

// ListTeamLive returns the team's Labs that were not destroyed.
func (r *Repository) ListTeamLive(ctx context.Context, teamID uuid.UUID) ([]labBindingModel.Binding, error) {
	rows, err := r.q.ListTeamInfrastructureLabs(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]labBindingModel.Binding, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToDomain(row))
	}
	return out, nil
}

// Recreate moves one Lab to the next generation under labName.
func (r *Repository) Recreate(ctx context.Context, value labBindingModel.Binding, labName string) (bool, error) {
	affected, err := r.q.RecreateLabBinding(ctx, postgres.RecreateLabBindingParams{ID: value.ID, Generation: value.Generation, LabName: labName})
	return affected == 1, err
}

// ResetUnpublishedForRecreate returns not yet published infrastructure
// challenges of the team to preparing.
func (r *Repository) ResetUnpublishedForRecreate(ctx context.Context, teamID uuid.UUID) error {
	return r.q.ResetUnpublishedTeamChallengesForRecreate(ctx, teamID)
}

func (r *Repository) MarkEventDestroyed(ctx context.Context, eventID uuid.UUID) error {
	return r.q.MarkEventLabBindingsDestroyed(ctx, eventID)
}

func ToDomain(row postgres.LabBinding) labBindingModel.Binding {
	value := labBindingModel.Binding{ID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, EventChallengeID: row.EventChallengeID, LabGroupName: row.LabGroupName, LabName: row.LabName, CreatedAt: row.CreatedAt, Readiness: labBindingModel.Readiness(row.Readiness), Generation: row.Generation, FailureReason: row.FailureReason.String}
	if row.DeployedAt.Valid {
		at := row.DeployedAt.Time
		value.DeployedAt = &at
	}
	return value
}
