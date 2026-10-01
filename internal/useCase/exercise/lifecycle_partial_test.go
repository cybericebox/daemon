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
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// The working copy is autosaved while the admin is still typing: only JSON
// shape is enforced (by binding); business rules wait for publish.
func TestSaveDraft_AcceptsIncompleteWorkingCopy(t *testing.T) {
	for name, variants := range map[string][]exerciseModel.Variant{
		"no variants": nil,
		"blank task and malformed flag": {
			{Tasks: []exerciseModel.Task{{Name: "", Difficulty: "", Flag: []string{"not a flag"}}}},
			{Tasks: nil},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			uc := newUC(q, newFakeMedia())
			exID := uuid.Must(uuid.NewV7())
			q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())
			q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil)
			var captured postgres.UpsertExerciseDraftParams
			expectUpsertCapture(t, q, &captured)

			view, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: variants})
			if err != nil {
				t.Fatalf("incomplete working copy must save: %v", err)
			}
			if len(view.Variants) != len(variants) || len(captured.Variants) == 0 {
				t.Fatalf("unexpected saved copy: view=%+v blob=%s", view.Variants, captured.Variants)
			}
		})
	}
}

func TestGetWorkingCopy(t *testing.T) {
	ctx := context.Background()
	t.Run("draft row wins", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := postgresMocks.NewMockQuerier(ctrl)
		uc := newUC(q, newFakeMedia())
		exID, draftID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, DraftVersionID: uuid.NullUUID{UUID: draftID, Valid: true}}, nil)
		q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{ID: draftID, ExerciseID: exID, Status: "draft", Variants: []byte(`[]`)}, nil)
		view, err := uc.GetWorkingCopy(ctx, exID)
		if err != nil || view.ID != draftID || view.Status != "draft" {
			t.Fatalf("view=%+v err=%v", view, err)
		}
	})
	t.Run("no draft row: published content", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := postgresMocks.NewMockQuerier(ctrl)
		uc := newUC(q, newFakeMedia())
		exID, publishedID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, PublishedVersionID: uuid.NullUUID{UUID: publishedID, Valid: true}}, nil)
		q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())
		q.EXPECT().GetExerciseVersionByID(gomock.Any(), publishedID).Return(postgres.ExerciseVersion{
			ID: publishedID, ExerciseID: exID, Status: "published", AdminNote: "live", Variants: mustMarshal(t, draftVariants("cipher", true)),
		}, nil)
		view, err := uc.GetWorkingCopy(ctx, exID)
		if err != nil || view.ID != publishedID || view.Status != "published" || view.AdminNote != "live" || len(view.Variants) != 1 {
			t.Fatalf("view=%+v err=%v", view, err)
		}
		if env := view.Variants[0].Topology.Devices[0].EnvVars[0]; env.Value != "" {
			t.Fatalf("secrets must stay masked: %+v", env)
		}
	})
	t.Run("nothing saved nor published: empty copy", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := postgresMocks.NewMockQuerier(ctrl)
		uc := newUC(q, newFakeMedia())
		exID := uuid.Must(uuid.NewV7())
		q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil)
		q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())
		view, err := uc.GetWorkingCopy(ctx, exID)
		if err != nil || view.ID != uuid.Nil || view.ExerciseID != exID || view.Status != "draft" || view.Variants == nil || len(view.Variants) != 0 {
			t.Fatalf("view=%+v err=%v", view, err)
		}
	})
	t.Run("missing exercise", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := postgresMocks.NewMockQuerier(ctrl)
		uc := newUC(q, newFakeMedia())
		exID := uuid.Must(uuid.NewV7())
		q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{}, errNoRows())
		if _, err := uc.GetWorkingCopy(ctx, exID); !errors.Is(err, exerciseModel.ErrExerciseNotFound.Err()) {
			t.Fatalf("want ErrExerciseNotFound, got %v", err)
		}
	})
}
