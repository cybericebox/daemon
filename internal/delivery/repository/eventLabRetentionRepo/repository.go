package eventLabRetentionRepo

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Queries interface {
	FinalizeRetiredLabGroupPlacement(context.Context, postgres.FinalizeRetiredLabGroupPlacementParams) (int64, error)
	IsOwnedRetainedLabReference(context.Context, postgres.IsOwnedRetainedLabReferenceParams) (bool, error)
	HasActiveEventLabRuntimeSelection(context.Context, postgres.HasActiveEventLabRuntimeSelectionParams) (bool, error)
	SetEventStageRetentionPreparationDue(context.Context, postgres.SetEventStageRetentionPreparationDueParams) error
	ListEventStageRuntimeMemberships(context.Context, uuid.UUID) ([]postgres.ListEventStageRuntimeMembershipsRow, error)
	ListDueRetainedEventLabs(context.Context, postgres.ListDueRetainedEventLabsParams) ([]postgres.EventTeamLab, error)
	ListRetiringEventLabs(context.Context, int32) ([]postgres.EventTeamLab, error)
	ListOrphanEventLabs(context.Context, int32) ([]postgres.EventTeamLab, error)
	HasEventLabRetentionPin(context.Context, postgres.HasEventLabRetentionPinParams) (bool, error)
	SelectEventStageRetainedLab(context.Context, postgres.SelectEventStageRetainedLabParams) error
	RemoveEventStageRuntimeSelections(context.Context, uuid.UUID) error
	RecomputeEventLabRetentionPins(context.Context, postgres.RecomputeEventLabRetentionPinsParams) error
	RecomputeEventGroupRetentionPins(context.Context, uuid.UUID) error
	RefreshEventLabProtectedUntil(context.Context, uuid.UUID) error
	RefreshEventGroupProtectedUntil(context.Context, uuid.UUID) error
	ListDueStageRuntimeSelections(context.Context, postgres.ListDueStageRuntimeSelectionsParams) ([]postgres.EventTeamLab, error)
	ArchiveEventLabGeneration(context.Context, uuid.UUID) error
	IsOwnedRetainedLabGroup(context.Context, string) (bool, error)
	ListDueRetainedGroups(context.Context, postgres.ListDueRetainedGroupsParams) ([]postgres.EventTeamGroupAllocation, error)
}
type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q} }
func labs(rows []postgres.EventTeamLab) ([]eventLabModel.Lab, error) {
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
func (r *Repository) DueLabs(ctx context.Context, now time.Time, limit int32) ([]eventLabModel.Lab, error) {
	rows, e := r.q.ListDueRetainedEventLabs(ctx, postgres.ListDueRetainedEventLabsParams{Now: pgtype.Timestamptz{Time: now, Valid: true}, LimitVal: limit})
	if e != nil {
		return nil, e
	}
	return labs(rows)
}
func (r *Repository) Retiring(ctx context.Context, limit int32) ([]eventLabModel.Lab, error) {
	rows, e := r.q.ListRetiringEventLabs(ctx, limit)
	if e != nil {
		return nil, e
	}
	return labs(rows)
}
func (r *Repository) Orphans(ctx context.Context, limit int32) ([]eventLabModel.Lab, error) {
	rows, e := r.q.ListOrphanEventLabs(ctx, limit)
	if e != nil {
		return nil, e
	}
	return labs(rows)
}
func (r *Repository) Pinned(ctx context.Context, l eventLabModel.Lab, now time.Time) (bool, error) {
	return r.q.HasEventLabRetentionPin(ctx, postgres.HasEventLabRetentionPinParams{LabID: l.ID, Generation: l.Generation, Now: now})
}
func (r *Repository) Select(ctx context.Context, eventID, stageID, labID uuid.UUID, now time.Time) error {
	return r.q.SelectEventStageRetainedLab(ctx, postgres.SelectEventStageRetainedLabParams{EventID: eventID, StageID: stageID, LabID: labID, Now: now})
}
func (r *Repository) Recompute(ctx context.Context, eventID uuid.UUID, preparation time.Duration, now time.Time) error {
	if e := r.q.RecomputeEventLabRetentionPins(ctx, postgres.RecomputeEventLabRetentionPinsParams{EventID: eventID, PreparationSeconds: int64((preparation + time.Second - 1) / time.Second), Now: now}); e != nil {
		return e
	}
	if e := r.q.RecomputeEventGroupRetentionPins(ctx, eventID); e != nil {
		return e
	}
	if e := r.q.RefreshEventLabProtectedUntil(ctx, eventID); e != nil {
		return e
	}
	return r.q.RefreshEventGroupProtectedUntil(ctx, eventID)
}
func (r *Repository) DueSelections(ctx context.Context, eventID uuid.UUID, now time.Time) ([]eventLabModel.Lab, error) {
	rows, e := r.q.ListDueStageRuntimeSelections(ctx, postgres.ListDueStageRuntimeSelectionsParams{EventID: eventID, Now: now})
	if e != nil {
		return nil, e
	}
	return labs(rows)
}
func (r *Repository) Archive(ctx context.Context, id uuid.UUID) error {
	return r.q.ArchiveEventLabGeneration(ctx, id)
}
func (r *Repository) OwnedGroup(ctx context.Context, name string) (bool, error) {
	return r.q.IsOwnedRetainedLabGroup(ctx, name)
}
func (r *Repository) DueGroups(ctx context.Context, now time.Time, limit int32) ([]eventLabModel.Group, error) {
	rows, e := r.q.ListDueRetainedGroups(ctx, postgres.ListDueRetainedGroupsParams{Now: pgtype.Timestamptz{Time: now, Valid: true}, LimitVal: limit})
	out := make([]eventLabModel.Group, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventLabAllocationRepo.ToGroupDomain(row))
	}
	return out, e
}

func (r *Repository) ActiveSelection(ctx context.Context, l eventLabModel.Lab, now time.Time) (bool, error) {
	return r.q.HasActiveEventLabRuntimeSelection(ctx, postgres.HasActiveEventLabRuntimeSelectionParams{LabID: l.ID, Generation: l.Generation, Now: now})
}
func (r *Repository) SetDue(ctx context.Context, stageID uuid.UUID, due time.Time) error {
	return r.q.SetEventStageRetentionPreparationDue(ctx, postgres.SetEventStageRetentionPreparationDueParams{StageID: stageID, NeededFrom: due})
}
