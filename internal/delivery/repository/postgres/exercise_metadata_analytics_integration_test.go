package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestAnalyticsAggregatesOneTaskAcrossPinnedMetadataVersions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	f := anSeed(t, db, "metadataanalyticsone")
	var exerciseID, taskID uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT ee.exercise_id, ec.task_id FROM event_exercises ee JOIN event_challenges ec ON ec.event_exercise_id = ee.id WHERE ec.id = $1`, f.challenge).Scan(&exerciseID, &taskID))
	identity, err := repo.GetByID(ctx, exerciseID)
	require.NoError(t, err)
	oldTime := identity.UpdatedAt
	identity.Name, identity.Tags, identity.UpdatedAt = "Z older pinned name", []string{"web"}, itNow.Add(time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, identity, oldTime, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	older, err := repo.Publish(ctx, exerciseID, itNow.Add(2*time.Minute))
	require.NoError(t, err)
	rtExec(t, db, `UPDATE event_exercises SET exercise_version_id = $2 WHERE id = (SELECT event_exercise_id FROM event_challenges WHERE id = $1)`, f.challenge, older.ID)
	rtExec(t, db, `UPDATE event_challenges SET published = true, snapshot = '{"name":"Z older subtask","difficulty":"easy"}'::jsonb WHERE id = $1`, f.challenge)
	g := anSeed(t, db, "metadataanalyticstwo")
	identity, err = repo.GetByID(ctx, exerciseID)
	require.NoError(t, err)
	oldTime = identity.UpdatedAt
	identity.Name, identity.Tags, identity.UpdatedAt = "A newer pinned name", []string{"crypto"}, itNow.Add(3*time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, identity, oldTime, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	newer, err := repo.Publish(ctx, exerciseID, itNow.Add(4*time.Minute))
	require.NoError(t, err)
	rtExec(t, db, `UPDATE event_exercises SET exercise_id = $2, exercise_version_id = $3 WHERE id = (SELECT event_exercise_id FROM event_challenges WHERE id = $1)`, g.challenge, exerciseID, newer.ID)
	rtExec(t, db, `UPDATE event_challenges SET task_id = $2, published = true, snapshot = '{"name":"A newer subtask","difficulty":"easy"}'::jsonb WHERE id = $1`, g.challenge, taskID)
	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(10*time.Minute))
	paSolve(t, db, f.teamChallenge, anStart.Add(10*time.Minute))
	anAttempt(t, db, g, g.team, g.teamChallenge, false, anStart.Add(2*time.Minute))
	anAttempt(t, db, g, g.team, g.teamChallenge, true, anStart.Add(4*time.Minute))
	paSolve(t, db, g.teamChallenge, anStart.Add(4*time.Minute))
	params := postgres.ListPlatformAnalyticsTaskCatalogParams{FromAt: anStart.Add(-time.Hour), ToAt: anStart.Add(time.Hour), RowLimit: 10}
	rows, err := db.Queries.ListPlatformAnalyticsTaskCatalog(ctx, params)
	require.NoError(t, err)
	require.Len(t, rows, 1, "retagging must not split the exercise/task identity or inflate the total")
	row := rows[0]
	require.Equal(t, exerciseID, row.ExerciseID)
	require.Equal(t, taskID, row.TaskID)
	require.EqualValues(t, 1, row.Total)
	require.EqualValues(t, 2, row.EventsUsed)
	require.EqualValues(t, 4, row.Attempts)
	require.EqualValues(t, 2, row.TeamsTried)
	require.EqualValues(t, 2, row.TeamsEngaged)
	require.EqualValues(t, 2, row.Solves)
	require.EqualValues(t, 330, row.MedianSolveSecs, "the median uses the same two-event population as the counts")
	require.Equal(t, newer.Name, row.ExerciseName)
	require.Equal(t, "A newer subtask", row.TaskName)
	require.Equal(t, newer.Tags, row.Tags, "display fields come together from the newest relevant pinned snapshot")
	for _, tc := range []struct {
		category, name, task string
		median               int64
	}{
		{"web", older.Name, "Z older subtask", 540},
		{"crypto", newer.Name, "A newer subtask", 120},
	} {
		params.Category = pgtype.Text{String: tc.category, Valid: true}
		filtered, queryErr := db.Queries.ListPlatformAnalyticsTaskCatalog(ctx, params)
		require.NoError(t, queryErr)
		require.Len(t, filtered, 1)
		require.EqualValues(t, 1, filtered[0].Total)
		require.EqualValues(t, 1, filtered[0].EventsUsed)
		require.EqualValues(t, 2, filtered[0].Attempts)
		require.EqualValues(t, 1, filtered[0].Solves)
		require.Equal(t, tc.median, filtered[0].MedianSolveSecs)
		require.Equal(t, tc.name, filtered[0].ExerciseName)
		require.Equal(t, tc.task, filtered[0].TaskName)
		require.Equal(t, []string{tc.category}, filtered[0].Tags)
	}
	categories, err := db.Queries.ListPlatformAnalyticsTaskCategories(ctx, postgres.ListPlatformAnalyticsTaskCategoriesParams{FromAt: params.FromAt, ToAt: params.ToAt})
	require.NoError(t, err)
	require.Equal(t, []string{"crypto", "web"}, categories)
}
