package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEditableImportTargetsFilterBeforePaginationAndKeepOwnershipContext(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	eventA := mustSeedEventForParticipants(t, db, "targetsa")
	eventB := mustSeedEventForParticipants(t, db, "targetsb")
	manager := mustSeedUser(t, db, "reimport-manager@test.test")
	viewer := mustSeedUser(t, db, "reimport-viewer@test.test")
	owner := mustSeedUser(t, db, "reimport-owner@test.test")
	for user, role := range map[uuid.UUID]int16{owner: 0, manager: 1, viewer: 2} {
		_, err := db.Queries.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: eventA.ID, UserID: user, Role: role, CreatedAt: itNow})
		require.NoError(t, err)
	}
	create := func(scope exerciseModel.Scope, event uuid.UUID, name string) exerciseModel.Exercise {
		e, err := exerciseModel.NewExercise(name, "", nil, uuid.Nil, itNow)
		require.NoError(t, err)
		e.Scope = scope
		if event != uuid.Nil {
			e.OwnerEventID = uuid.NullUUID{UUID: event, Valid: true}
		}
		e, err = repo.Create(ctx, e)
		require.NoError(t, err)
		return e
	}
	own := create(exerciseModel.ScopeEvent, eventA.ID, "Repeated task")
	renamed := own
	renamed.Name = "Working task name"
	renamed.Tags = []string{"web"}
	_, err := repo.SaveDraftIdentity(ctx, renamed, own.UpdatedAt, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	// All denied rows are newer than own: filtering after LIMIT 1 would hide it.
	catalog := create(exerciseModel.ScopeCatalog, uuid.Nil, "Repeated task")
	foreign := create(exerciseModel.ScopeEvent, eventB.ID, "Repeated task")
	archived := create(exerciseModel.ScopeEvent, eventA.ID, "Archived task")
	_, err = db.Pool.Exec(ctx, `UPDATE exercises SET archived_at=$2 WHERE id=$1`, archived.ID, itNow)
	require.NoError(t, err)
	for _, user := range []uuid.UUID{manager, owner} {
		args := postgres.ListEditableImportTargetsParams{ViewerID: user, Search: "Working", LimitVal: 1}
		rows, err := db.Queries.ListEditableImportTargets(ctx, args)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, own.ID, rows[0].ID)
		require.True(t, rows[0].HasDraft)
		require.NotEmpty(t, rows[0].OwnerEventName)
		require.NotEmpty(t, rows[0].OwnerEventTag)
		count, err := db.Queries.CountEditableImportTargets(ctx, postgres.CountEditableImportTargetsParams{ViewerID: user})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	}
	rows, err := db.Queries.ListEditableImportTargets(ctx, postgres.ListEditableImportTargetsParams{ViewerID: viewer, LimitVal: 20})
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = db.Queries.ListEditableImportTargets(ctx, postgres.ListEditableImportTargetsParams{WriterAll: true, LimitVal: 2})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, foreign.ID, rows[0].ID)
	require.Equal(t, catalog.ID, rows[1].ID)
	require.Equal(t, "Repeated task", rows[0].Name)
	require.Equal(t, rows[0].Name, rows[1].Name)
	require.True(t, rows[0].OwnerEventID.Valid)
	require.False(t, rows[1].OwnerEventID.Valid)
	next, err := db.Queries.ListEditableImportTargets(ctx, postgres.ListEditableImportTargetsParams{WriterAll: true, CursorID: uuid.NullUUID{UUID: rows[1].ID, Valid: true}, LimitVal: 2})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, own.ID, next[0].ID)
	count, err := db.Queries.CountEditableImportTargets(ctx, postgres.CountEditableImportTargetsParams{WriterAll: true})
	require.NoError(t, err)
	require.EqualValues(t, 3, count)
}
