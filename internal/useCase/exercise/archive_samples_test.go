package exercise_test

import (
	"context"
	"os"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// testdata/static.zip is a copy of dist/static.zip of the test-exercises repository.
func TestSampleArchive_StaticImports(t *testing.T) {
	archive, err := os.ReadFile("testdata/static.zip")
	require.NoError(t, err)
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectUserNames(q)
	expectFilesAdoptedAsIs(q)
	media := newFakeMedia()
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			require.Equal(t, "Загублений пакет", arg.Name)
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			require.EqualValues(t, exerciseModel.VersionStatusPublished, arg.Status)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})

	imported, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{
		Archive:   archive,
		CreatedBy: uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"),
	})
	require.NoError(t, err)
	require.Equal(t, "Загублений пакет", imported.Name)
	require.Len(t, media.uploads, 1)
	require.Equal(t, "welcome.go", media.uploads[0].Name)
}
