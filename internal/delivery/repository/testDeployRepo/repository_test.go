package testDeployRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	testDeployRepo "github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestRepository_CreateAndGetOwned(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	id := uuid.Must(uuid.NewV7())
	v := exerciseModel.TestDeploy{ID: id, VersionID: uuid.Must(uuid.NewV7()), VariantID: uuid.Must(uuid.NewV7()), CreatedBy: uuid.Must(uuid.NewV7()), GroupName: "t-1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	repo := testDeployRepo.New(q)
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(testDeployRepo.ToRow(v), nil)
	created, err := repo.Create(context.Background(), v)
	require.NoError(t, err)
	require.Equal(t, v.ID, created.ID)
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(testDeployRepo.ToRow(v), nil)
	got, err := repo.GetOwned(context.Background(), id, v.CreatedBy)
	require.NoError(t, err)
	require.Equal(t, v.GroupName, got.GroupName)
}

func TestRepository_FlagsSurviveTheRoundTrip(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	repo := testDeployRepo.New(q)
	v := exerciseModel.TestDeploy{ID: uuid.Must(uuid.NewV7()), CreatedBy: uuid.Must(uuid.NewV7()), GroupName: "t-2",
		Flags: []exerciseModel.DeployFlag{{TaskID: uuid.Must(uuid.NewV7()), Name: "Login", Flag: "ICE{a}"}}}
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		require.JSONEq(t, `[{"task_id":"`+v.Flags[0].TaskID.String()+`","name":"Login","flag":"ICE{a}"}]`, string(p.Flags))
		return postgres.ExerciseTestDeployment{ID: p.ID, Flags: p.Flags}, nil
	})
	got, err := repo.Create(context.Background(), v)
	require.NoError(t, err)
	require.Equal(t, v.Flags, got.Flags)

	q.EXPECT().ListOwnedExerciseTestDeploysForExercise(gomock.Any(), gomock.Any()).Return([]postgres.ExerciseTestDeployment{{ID: v.ID, Flags: []byte("garbage")}}, nil)
	list, err := repo.ListOwnedForExercise(context.Background(), v.CreatedBy, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Empty(t, list[0].Flags, "a corrupt stored value is forgiven")
}
