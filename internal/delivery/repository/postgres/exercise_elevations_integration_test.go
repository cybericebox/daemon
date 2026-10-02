package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestResourceElevationLifecycleInTheDatabase(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex, other := mustCreateExercise(t, repo, "IT elevation"), mustCreateExercise(t, repo, "IT other")
	device := resourcesModel.Approval{DeviceID: uuid.Must(uuid.NewV7()), Name: "db", Amount: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 << 30}}

	_, err := repo.LatestElevation(ctx, ex.ID)
	require.True(t, errors.Is(err, pgx.ErrNoRows) || repositoryTools.IsObjectNotFoundError(err), "no request yet: %v", err)

	first, err := resourcesModel.NewElevation(ex.ID, uuid.NullUUID{}, "big db", []resourcesModel.Approval{device}, uuid.NullUUID{}, itNow)
	require.NoError(t, err)
	require.NoError(t, repo.CreateElevation(ctx, first))

	again, err := resourcesModel.NewElevation(ex.ID, uuid.NullUUID{}, "again", []resourcesModel.Approval{device}, uuid.NullUUID{}, itNow)
	require.NoError(t, err)
	err = repo.CreateElevation(ctx, again)
	_, unique := repositoryTools.UniqueViolationError(err, exerciseModel.ErrElevationPending)
	require.True(t, unique, "one pending request per exercise: %v", err)
	require.NoError(t, repo.CreateElevation(ctx, mustElevation(t, other.ID, device)), "another exercise has its own")

	approved, err := repo.ApprovedFor(ctx, []uuid.UUID{ex.ID})
	require.NoError(t, err)
	require.Empty(t, approved[ex.ID], "pending is not approved")

	loaded, err := repo.GetElevation(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, []resourcesModel.Approval{device}, loaded.Requested)
	require.NoError(t, loaded.Approve(nil, resourcesModel.DefaultPolicy().Ceiling, uuid.Nil, "ok", itNow))
	n, err := repo.DecideElevation(ctx, loaded)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = repo.DecideElevation(ctx, loaded)
	require.NoError(t, err)
	require.EqualValues(t, 0, n, "a decided request is not decided twice")

	approved, err = repo.ApprovedFor(ctx, []uuid.UUID{ex.ID, other.ID})
	require.NoError(t, err)
	require.Equal(t, []resourcesModel.Approval{device}, approved[ex.ID])
	require.Empty(t, approved[other.ID])

	latest, err := repo.LatestElevation(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, resourcesModel.ElevationApproved, latest.Status)
	require.NotNil(t, latest.DecidedAt)
	require.NoError(t, repo.CreateElevation(ctx, again), "after a decision a new request may be filed")
	latest, err = repo.LatestElevation(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, resourcesModel.ElevationPending, latest.Status, "the open request comes first")

	pending := resourcesModel.ElevationPending
	list, err := repo.ListElevations(ctx, &pending, uuid.NullUUID{})
	require.NoError(t, err)
	require.Len(t, list, 2)
	byExercise, err := repo.ListElevations(ctx, nil, uuid.NullUUID{UUID: ex.ID, Valid: true})
	require.NoError(t, err)
	require.Len(t, byExercise, 2)
	require.Equal(t, "IT elevation", byExercise[0].ExerciseName)
}

func mustElevation(t *testing.T, exerciseID uuid.UUID, d resourcesModel.Approval) resourcesModel.Elevation {
	t.Helper()
	e, err := resourcesModel.NewElevation(exerciseID, uuid.NullUUID{}, "why", []resourcesModel.Approval{d}, uuid.NullUUID{}, itNow)
	require.NoError(t, err)
	return e
}
