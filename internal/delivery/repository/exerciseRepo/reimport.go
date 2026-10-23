package exerciseRepo

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

type ImportTargetParams struct {
	WriterAll bool
	ViewerID  uuid.UUID
	Search    string
	CursorID  uuid.UUID
	Limit     int32
}

type ImportTarget struct {
	ID                            uuid.UUID
	Name                          string
	Scope                         exerciseModel.Scope
	OwnerEventID                  uuid.NullUUID
	OwnerEventName, OwnerEventTag string
	HasDraft                      bool
}

func (r *Repository) ImportTargets(ctx context.Context, p ImportTargetParams) ([]ImportTarget, error) {
	rows, err := r.q.ListEditableImportTargets(ctx, postgres.ListEditableImportTargetsParams{
		WriterAll: p.WriterAll, ViewerID: p.ViewerID, Search: p.Search, CursorID: uuid.NullUUID{UUID: p.CursorID, Valid: p.CursorID != uuid.Nil}, LimitVal: p.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ImportTarget, 0, len(rows))
	for _, row := range rows {
		out = append(out, ImportTarget{ID: row.ID, Name: row.Name, Scope: exerciseModel.Scope(row.Scope), OwnerEventID: row.OwnerEventID, OwnerEventName: row.OwnerEventName, OwnerEventTag: row.OwnerEventTag, HasDraft: row.HasDraft})
	}
	return out, nil
}

func (r *Repository) CountImportTargets(ctx context.Context, p ImportTargetParams) (int64, error) {
	return r.q.CountEditableImportTargets(ctx, postgres.CountEditableImportTargetsParams{WriterAll: p.WriterAll, ViewerID: p.ViewerID, Search: p.Search})
}

func (r *Repository) ImportTargetContext(ctx context.Context, id uuid.UUID) (string, string, error) {
	row, err := r.q.GetImportTargetContext(ctx, id)
	return row.OwnerEventName, row.OwnerEventTag, err
}
