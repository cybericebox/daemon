package exercise_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestIdentityPatchSavesWorkingMetadataWithoutUpdatingPublishedIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	exID, draftID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	before := time.Now().Add(-time.Hour)
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, Name: "Published", UpdatedAt: before}, nil)
	q.EXPECT().SaveExerciseDraftIdentity(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.SaveExerciseDraftIdentityParams) (postgres.SaveExerciseDraftIdentityRow, error) {
		require.Equal(t, before, p.ExpectedUpdatedAt)
		require.Equal(t, "Draft name", p.Name)
		return postgres.SaveExerciseDraftIdentityRow{ID: draftID, ExerciseID: exID, Status: "draft", Name: p.Name, Description: p.Description, Tags: p.Tags, Variants: []byte(`[]`)}, nil
	})
	view, err := newUC(q, newFakeMedia()).UpdateExerciseIdentity(context.Background(), exercise.UpdateExerciseInput{ID: exID, Name: "Draft name", Description: "Draft description", Tags: []string{"Web"}})
	require.NoError(t, err)
	require.Equal(t, "Draft name", view.Name)
	require.Equal(t, []string{"web"}, view.Tags)
	require.Equal(t, draftID, *view.DraftVersionID)
	require.True(t, view.HasChanges)
}

func TestExerciseMetadataCardProjectsWorkingSnapshotOnlyForFullReaders(t *testing.T) {
	for _, role := range []rbac.Role{rbac.RoleSuperAdmin, rbac.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			exID, draftID, publishedID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			entry := postgres.Exercise{ID: exID, Name: "Published", Tags: []string{"web"}, DraftVersionID: uuid.NullUUID{UUID: draftID, Valid: true}, PublishedVersionID: uuid.NullUUID{UUID: publishedID, Valid: true}}
			q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(entry, nil).AnyTimes()
			q.EXPECT().ExerciseDraftDiffersFromPublished(gomock.Any(), exID).Return(true, nil)
			q.EXPECT().ListExerciseCardExtras(gomock.Any(), gomock.Any()).Return(nil, nil)
			q.EXPECT().ListExercisesEventAccess(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			q.EXPECT().ListVisibleVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			q.EXPECT().ListApprovedExerciseResourceElevations(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			// Every reader's event roles are read (an admin who manages the owner event may propose).
			q.EXPECT().ListUserEventMemberships(gomock.Any(), gomock.Any()).Return(nil, nil)
			if role == rbac.RoleSuperAdmin {
				q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{ID: draftID, ExerciseID: exID, Status: "draft", Name: "Working", Tags: []string{"crypto"}, Variants: []byte(`[]`)}, nil)
			}
			view, err := newUC(q, newFakeMedia()).GetExerciseFor(context.Background(), exercise.Actor{UserID: uuid.Must(uuid.NewV7()), Role: role}, exID)
			require.NoError(t, err)
			if role == rbac.RoleSuperAdmin {
				require.Equal(t, "Working", view.Name)
				require.Equal(t, []string{"crypto"}, view.Tags)
			} else {
				require.Equal(t, "Published", view.Name)
				require.Equal(t, []string{"web"}, view.Tags)
			}
		})
	}
}

func TestArchiveRejectsInvalidPerVersionMetadataBeforeAnyTaskWrite(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, `{"name":"Valid root identity"}`, `[{"status":"checkpoint","exercise":{"name":"ab","description":"invalid snapshot name"},"variants":[]},{"status":"draft","variants":[]}]`)
	_, err := newUC(q, newFakeMedia()).ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.ErrorIs(t, err, exerciseModel.ErrExerciseNameInvalid.Err())
}

func TestPublishMetadataNameCollisionStaysWithTheNameField(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	id := uuid.Must(uuid.NewV7())
	variants := []exerciseModel.Variant{{ID: uuid.Must(uuid.NewV7()), Tasks: []exerciseModel.Task{{
		ID: uuid.Must(uuid.NewV7()), Name: "Valid static task", Difficulty: exerciseModel.DifficultyEasy,
		Description: json.RawMessage(`{"root":{"type":"text","text":"Solve this task"}}`), Flag: []string{"ICE{x}"},
	}}}}
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id, Name: "Published name"}, nil)
	q.EXPECT().GetDraftVersion(gomock.Any(), id).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: id, Status: "draft", Name: "Occupied name", Variants: mustMarshal(t, variants),
	}, nil)
	q.EXPECT().PublishExerciseDraft(gomock.Any(), gomock.Any()).Return(postgres.PublishExerciseDraftRow{}, &pgconnUniqueViolation)
	_, err := newUC(q, newFakeMedia()).PublishDraft(context.Background(), id)
	require.ErrorIs(t, err, exerciseModel.ErrExerciseExists.Err())
	require.Equal(t, "Name", publicContext(t, err)["field"])
}
