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
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	userModel "github.com/cybericebox/daemon/internal/model/user"
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
	require.NoError(t, loaded.Approve(nil, resourcesModel.DefaultPolicy(), uuid.Nil, "ok", itNow))
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
	list, err := repo.ListElevations(ctx, resourcesModel.ElevationFilter{Status: &pending})
	require.NoError(t, err)
	require.Len(t, list, 2)
	byExercise, err := repo.ListElevations(ctx, resourcesModel.ElevationFilter{ExerciseID: uuid.NullUUID{UUID: ex.ID, Valid: true}})
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

// The author's history is the list narrowed by author; the pending count follows the decisions.
func TestElevationHistoryByAuthorAndPendingCount(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	users := userRepo.New(db.Queries)
	ann, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "ann-elev@test.test", itNow))
	require.NoError(t, err)
	bob, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "bob-elev@test.test", itNow))
	require.NoError(t, err)
	device := resourcesModel.Approval{DeviceID: uuid.Must(uuid.NewV7()), Name: "db", Amount: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 << 30}}
	file := func(exerciseID, by uuid.UUID) resourcesModel.Elevation {
		e, newErr := resourcesModel.NewElevation(exerciseID, uuid.NullUUID{}, "why", []resourcesModel.Approval{device}, uuid.NullUUID{UUID: by, Valid: true}, itNow)
		require.NoError(t, newErr)
		require.NoError(t, repo.CreateElevation(ctx, e))
		return e
	}
	first := file(mustCreateExercise(t, repo, "History A").ID, ann.ID)
	file(mustCreateExercise(t, repo, "History B").ID, ann.ID)
	file(mustCreateExercise(t, repo, "History C").ID, bob.ID)

	byAnn, err := repo.ListElevations(ctx, resourcesModel.ElevationFilter{RequestedBy: uuid.NullUUID{UUID: ann.ID, Valid: true}})
	require.NoError(t, err)
	require.Len(t, byAnn, 2, "only Ann's requests")
	all, err := repo.ListElevations(ctx, resourcesModel.ElevationFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)

	n, err := repo.CountPendingElevations(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	decided, err := repo.GetElevation(ctx, first.ID)
	require.NoError(t, err)
	require.NoError(t, decided.Reject(uuid.Nil, "no", itNow))
	_, err = repo.DecideElevation(ctx, decided)
	require.NoError(t, err)
	n, err = repo.CountPendingElevations(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, n, "a decided request no longer counts")
}
