package inboxUseCase

import (
	"context"
	"strconv"
	"time"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// ImportEnd is the end of a background task import, as the exercise use case reports it.
type ImportEnd struct {
	// User started the import: the only recipient.
	User uuid.UUID
	// Created is how many tasks the import created (done only).
	Created int
	At      time.Time
}

// ExerciseImportDone tells the user who started an import how many tasks it created.
func (r *RequestRouter) ExerciseImportDone(ctx context.Context, e ImportEnd) error {
	meta := inboxModel.NewMeta(inboxModel.TypeExerciseImportDone, inboxModel.RoleSubject, "").Raised(e.At)
	return r.fanOut(ctx, []uuid.UUID{e.User}, inboxModel.TypeExerciseImportDone, map[string]any{"count": strconv.Itoa(e.Created)}, meta)
}

// ExerciseImportFailed tells the user who started an import that it failed and created nothing.
func (r *RequestRouter) ExerciseImportFailed(ctx context.Context, e ImportEnd) error {
	meta := inboxModel.NewMeta(inboxModel.TypeExerciseImportFailed, inboxModel.RoleSubject, "").Raised(e.At)
	return r.fanOut(ctx, []uuid.UUID{e.User}, inboxModel.TypeExerciseImportFailed, map[string]any{}, meta)
}
