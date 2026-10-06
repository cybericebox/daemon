package exercise_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

var archiveLoadedAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func TestArchiveExercise_WritesArchivedAtInWholeRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectUserNames(q)
	uc := newUC(q, newFakeMedia())
	exID, adminID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{
		ID: exID, Name: "Keep me", CreatedAt: archiveLoadedAt, UpdatedAt: archiveLoadedAt,
	}, nil)
	q.EXPECT().UpdateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateExerciseParams) (int64, error) {
			if !arg.ArchivedAt.Valid || arg.Name != "Keep me" || !arg.ExpectedUpdatedAt.Equal(archiveLoadedAt) ||
				!arg.UpdatedBy.Valid || arg.UpdatedBy.UUID != adminID {
				t.Fatalf("unexpected archive write: %+v", arg)
			}
			return 1, nil
		})

	view, err := uc.ArchiveExercise(context.Background(), exID, adminID)
	if err != nil || view.ArchivedAt == nil || !view.HasChanges {
		t.Fatalf("archived card: %+v err=%v", view, err)
	}
}

func TestUnarchiveExercise_ClearsArchivedAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{
		ID: exID, Name: "Back again", UpdatedAt: archiveLoadedAt,
		ArchivedAt: pgtype.Timestamptz{Time: archiveLoadedAt, Valid: true},
	}, nil)
	q.EXPECT().UpdateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateExerciseParams) (int64, error) {
			if arg.ArchivedAt.Valid {
				t.Fatalf("unarchive must clear archived_at: %+v", arg)
			}
			return 1, nil
		})

	view, err := uc.UnarchiveExercise(context.Background(), exID, uuid.Nil)
	if err != nil || view.ArchivedAt != nil {
		t.Fatalf("unarchived card: %+v err=%v", view, err)
	}
}

func TestArchivedExercise_RejectsMutations(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error{
		"save draft": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			_, err := uc.SaveDraft(ctx, exID, exercise.SaveDraftInput{})
			return err
		},
		"publish": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			_, err := uc.PublishDraft(ctx, exID)
			return err
		},
		"discard": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			return uc.DiscardDraft(ctx, exID)
		},
		"rollback": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			_, err := uc.RollbackToVersion(ctx, exID, uuid.Must(uuid.NewV7()), uuid.Nil)
			return err
		},
		"checkpoint": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			_, err := uc.CreateCheckpoint(ctx, exID, uuid.Nil, "")
			return err
		},
		"restore": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			_, err := uc.RestoreToVersion(ctx, exID, uuid.Must(uuid.NewV7()), uuid.Nil)
			return err
		},
		"rename": func(uc *exercise.ExerciseUseCase, exID uuid.UUID) error {
			_, err := uc.UpdateExerciseIdentity(ctx, exercise.UpdateExerciseInput{ID: exID, Name: "Renamed exercise"})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			exID := uuid.Must(uuid.NewV7())
			q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{
				ID: exID, Name: "Archived one", ArchivedAt: pgtype.Timestamptz{Time: archiveLoadedAt, Valid: true},
			}, nil)
			if err := call(newUC(q, newFakeMedia()), exID); !errors.Is(err, exerciseModel.ErrExerciseArchived.Err()) {
				t.Fatalf("want ErrExerciseArchived, got %v", err)
			}
		})
	}
}
