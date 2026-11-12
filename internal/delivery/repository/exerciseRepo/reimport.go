package exerciseRepo

import (
	"context"
	"time"

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

// ActiveAttachment is an active attachment of an exercise in an event: what the event runs now.
type ActiveAttachment struct {
	ID                uuid.UUID
	EventID           uuid.UUID
	EventName         string
	Archived          bool
	ExerciseVersionID uuid.UUID
	FixedVariantIndex *int32
	RunningLabs       int
}

// ActiveAttachments lists the active attachments of the exercise in all events.
func (r *Repository) ActiveAttachments(
	ctx context.Context,
	exerciseID uuid.UUID,
	now time.Time,
) ([]ActiveAttachment, error) {
	rows, err := r.q.ListExerciseActiveAttachments(
		ctx,
		postgres.ListExerciseActiveAttachmentsParams{ExerciseID: exerciseID, Now: now},
	)
	if err != nil {
		return nil, err
	}
	out := make([]ActiveAttachment, 0, len(rows))
	for _, row := range rows {
		item := ActiveAttachment{
			ID:                row.EventExerciseID,
			EventID:           row.EventID,
			EventName:         row.EventName,
			Archived:          row.Archived,
			ExerciseVersionID: row.ExerciseVersionID,
			RunningLabs:       int(row.RunningLabs),
		}
		if row.FixedVariantIndex.Valid && row.VariantMode == 1 {
			index := row.FixedVariantIndex.Int32
			item.FixedVariantIndex = &index
		}
		out = append(out, item)
	}
	return out, nil
}

// AnswerCount is what an event recorded on one challenge: every attempt and the teams that solved it.
type AnswerCount struct{ Attempts, Solves int }

// AnswerCounts maps (attachment, task id) to the recorded answers of that board challenge.
func (r *Repository) AnswerCounts(
	ctx context.Context,
	exerciseID uuid.UUID,
) (map[[2]uuid.UUID]AnswerCount, error) {
	rows, err := r.q.ListExerciseChallengeAnswerCounts(ctx, exerciseID)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]uuid.UUID]AnswerCount, len(rows))
	for _, row := range rows {
		out[[2]uuid.UUID{row.EventExerciseID, row.TaskID}] = AnswerCount{
			Attempts: int(row.Attempts),
			Solves:   int(row.Solves),
		}
	}
	return out, nil
}

// LeasedTestDeploys counts the test labs of the exercise that are leased now.
func (r *Repository) LeasedTestDeploys(
	ctx context.Context,
	exerciseID uuid.UUID,
	now time.Time,
) (int, error) {
	n, err := r.q.CountLeasedExerciseTestDeploys(
		ctx,
		postgres.CountLeasedExerciseTestDeploysParams{ExerciseID: exerciseID, Now: now},
	)
	return int(n), err
}
