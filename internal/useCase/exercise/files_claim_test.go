package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

func variantsWithFiles(files ...uuid.UUID) []exerciseModel.Variant {
	refs := make([]exerciseModel.AttachmentRef, 0, len(files))
	for i, f := range files {
		refs = append(refs, exerciseModel.AttachmentRef{FileID: f, Name: string(rune('a'+i)) + ".zip"})
	}
	return []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Name: "t", Attachments: refs}}}}
}

// The reported PoC: an event manager puts another event's file id into their own exercise draft and then
// downloads the bytes through the exercise file route.
func TestSaveDraftRefusesAFileThatIsNotTheCallersAndNotTheExercises(t *testing.T) {
	ctx := context.Background()
	exID, me, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	mine, theirs, held, ghost := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	setup := func(t *testing.T) (*exercise.ExerciseUseCase, *postgresMocks.MockQuerier) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		expectUserNames(q)
		q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
		q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil).AnyTimes()
		q.EXPECT().ListFileOwners(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []uuid.UUID) ([]postgres.ListFileOwnersRow, error) {
			owners := map[uuid.UUID]uuid.UUID{mine: me, theirs: other, held: other}
			var rows []postgres.ListFileOwnersRow
			for _, id := range ids {
				if owner, ok := owners[id]; ok {
					rows = append(rows, postgres.ListFileOwnersRow{ID: id, CreatedBy: uuid.NullUUID{UUID: owner, Valid: true}})
				}
			}
			return rows, nil
		}).AnyTimes()
		q.EXPECT().ListFileExerciseIDs(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.ListFileExerciseIDsParams) ([]uuid.UUID, error) {
			if arg.FileID == held {
				return []uuid.UUID{exID}, nil // the exercise already holds it
			}
			return []uuid.UUID{uuid.Must(uuid.NewV7())}, nil // another exercise's
		}).AnyTimes()
		return newUC(q, newFakeMedia()), q
	}

	t.Run("another user's file by id", func(t *testing.T) {
		uc, _ := setup(t)
		_, err := uc.SaveDraft(ctx, exID, exercise.SaveDraftInput{SavedBy: me, Variants: variantsWithFiles(mine, theirs)})
		if !errors.Is(err, mediaModel.ErrFileNotFound.Err()) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a file that does not exist", func(t *testing.T) {
		uc, _ := setup(t)
		if _, err := uc.SaveDraft(ctx, exID, exercise.SaveDraftInput{SavedBy: me, Variants: variantsWithFiles(ghost)}); !errors.Is(err, mediaModel.ErrFileNotFound.Err()) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the caller's own and the exercise's own files", func(t *testing.T) {
		uc, q := setup(t)
		var captured postgres.UpsertExerciseDraftParams
		expectUpsertCapture(t, q, &captured)
		if _, err := uc.SaveDraft(ctx, exID, exercise.SaveDraftInput{SavedBy: me, Variants: variantsWithFiles(mine, held, mine)}); err != nil {
			t.Fatalf("own and held files must save: %v", err)
		}
	})
	t.Run("nothing attached needs no lookup", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())
		q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil)
		expectUserNames(q)
		var captured postgres.UpsertExerciseDraftParams
		expectUpsertCapture(t, q, &captured)
		if _, err := newUC(q, newFakeMedia()).SaveDraft(ctx, exID, exercise.SaveDraftInput{SavedBy: me, Variants: variantsWithFiles()}); err != nil {
			t.Fatal(err)
		}
	})
}
