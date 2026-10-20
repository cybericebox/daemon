package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestRollingLegacyCatalogMutationsFailBeforeAuthoredStateChanges(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExerciseFull(t, repo, "Rolling published name", "published description", []string{"web"}, itNow)
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	published, err := repo.Publish(ctx, ex.ID, itNow)
	require.NoError(t, err)
	file, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: "preserved.txt", ContentType: "text/plain", SizeBytes: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: itNow})
	require.NoError(t, err)
	require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: published.ID, FileIds: []uuid.UUID{file.ID}}))
	_, err = repo.ClaimFile(ctx, ex.ID, file.ContentHash, file.ID, itNow, itNow.Add(-time.Hour))
	require.NoError(t, err)
	ex.Name, ex.Description, ex.Tags, ex.UpdatedAt = "New capable draft name", "draft description", []string{"crypto"}, itNow.Add(time.Minute)
	draft, err := repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	event := mustSeedEventForParticipants(t, db, "rollingguard")
	legacyConfig := db.Pool.Config()
	delete(legacyConfig.ConnConfig.RuntimeParams, "cybericebox.catalog_metadata_writer")
	legacy, err := pgxpool.NewWithConfig(ctx, legacyConfig)
	require.NoError(t, err)
	defer legacy.Close()
	var marker *string
	require.NoError(t, legacy.QueryRow(ctx, `SELECT current_setting('cybericebox.catalog_metadata_writer', true)`).Scan(&marker))
	require.True(t, marker == nil || *marker == "", "this must be a real unmarked old connection")
	snapshot := func() string {
		t.Helper()
		var state string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT jsonb_build_object(
		 'parents', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM exercises e),
		 'versions', (SELECT jsonb_agg(to_jsonb(v) ORDER BY v.id) FROM exercise_versions v),
		 'claims', (SELECT jsonb_agg(to_jsonb(f) ORDER BY f.exercise_id, f.content_hash) FROM exercise_files f),
		 'refs', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.ref_type, r.ref_id, r.file_id) FROM file_references r),
		 'acl', (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.exercise_id, a.event_id) FROM exercise_event_access a))::text`).Scan(&state))
		return state
	}
	before := snapshot()
	for _, tc := range []struct {
		name, query string
		args        []any
	}{
		{"old copy/import first parent", `INSERT INTO exercises(id,name,created_at,updated_at) VALUES($1,'Legacy copy',$2,$2)`, []any{uuid.Must(uuid.NewV7()), itNow}},
		{"old version insert", `INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at) VALUES($1,$2,'checkpoint','[]',$3)`, []any{uuid.Must(uuid.NewV7()), ex.ID, itNow}},
		{"old metadata patch", `UPDATE exercises SET name='Legacy rename',description='legacy description',tags='{misc}',updated_at=$2 WHERE id=$1`, []any{ex.ID, itNow.Add(2 * time.Minute)}},
		{"old policy whole identity", `UPDATE exercises SET name='Rolling published name',description='published description',tags='{web}',access_level=3,updated_at=$2 WHERE id=$1`, []any{ex.ID, itNow.Add(2 * time.Minute)}},
		{"old publish", `WITH demoted AS (UPDATE exercise_versions SET status='unpublished' WHERE id=$1 RETURNING id), promoted AS (UPDATE exercise_versions SET status='published',published_at=$3 WHERE id=$2 AND (SELECT count(*) FROM demoted) IS NOT NULL RETURNING id) UPDATE exercises SET published_version_id=(SELECT id FROM promoted),draft_version_id=NULL WHERE id=$4`, []any{published.ID, draft.ID, itNow.Add(2 * time.Minute), ex.ID}},
		{"old restore/checkpoint first write", `INSERT INTO exercise_versions(id,exercise_id,status,admin_note,variants,created_at) SELECT $1,exercise_id,'checkpoint',admin_note,variants,$3 FROM exercise_versions WHERE id=$2`, []any{uuid.Must(uuid.NewV7()), draft.ID, itNow}},
		{"old adoption before draft save", `INSERT INTO exercise_files(exercise_id,content_hash,file_id,created_at) VALUES($1,$2,$3,$4) ON CONFLICT(exercise_id,content_hash) DO UPDATE SET file_id=excluded.file_id`, []any{ex.ID, file.ContentHash, file.ID, itNow}},
		{"old reference cleanup", `DELETE FROM file_references WHERE ref_type='exercise_version' AND ref_id=$1`, []any{draft.ID}},
		{"old ACL mutation", `INSERT INTO exercise_event_access(exercise_id,event_id) VALUES($1,$2)`, []any{ex.ID, event.ID}},
		{"old parent delete", `DELETE FROM exercises WHERE id=$1`, []any{ex.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, beginErr := legacy.Begin(ctx)
			require.NoError(t, beginErr)
			defer tx.Rollback(context.Background())
			_, writeErr := tx.Exec(ctx, tc.query, tc.args...)
			var pgErr *pgconn.PgError
			require.ErrorAs(t, writeErr, &pgErr)
			require.Equal(t, "40001", pgErr.Code)
			require.Contains(t, pgErr.Message, "catalog_metadata_writer")
			require.NoError(t, tx.Rollback(ctx))
			require.JSONEq(t, before, snapshot(), "legacy failure must retain all existing family, metadata, ACL, attachment claims and references")
		})
	}
	_, err = legacy.Exec(ctx, `UPDATE events SET infrastructure_allowed = infrastructure_allowed WHERE id=$1`, event.ID)
	require.NoError(t, err, "unrelated event runtime writes stay available to an old connection")
	newPublished, err := repo.Publish(ctx, ex.ID, itNow.Add(3*time.Minute))
	require.NoError(t, err)
	require.Equal(t, ex.Name, newPublished.Name)
	old, err := repo.GetVersion(ctx, published.ID)
	require.NoError(t, err)
	require.Equal(t, published.Name, old.Name)
	require.Equal(t, published.Description, old.Description)
	require.Equal(t, published.Tags, old.Tags)
}

func TestCapableVersionInsertStillRequiresExplicitCompleteMetadata(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	ex := mustCreateExercise(t, exerciseRepo.New(db.Queries), "Explicit metadata required")
	for _, metadata := range []string{
		``, `,name`, `,name,description`,
	} {
		var extraValues string
		if metadata == ",name" {
			extraValues = `,'Explicit name'`
		} else if metadata == ",name,description" {
			extraValues = `,'Explicit name',''`
		}
		_, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at`+metadata+`) VALUES($1,$2,'checkpoint','[]',$3`+extraValues+`)`, uuid.Must(uuid.NewV7()), ex.ID, itNow)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, "the capability is not a substitute for complete authored fields")
		require.Equal(t, "23502", pgErr.Code)
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint','[]',$3,'','','{}')`, uuid.Must(uuid.NewV7()), ex.ID, itNow)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	_, err = db.Pool.Exec(ctx, `INSERT INTO exercise_versions(id,exercise_id,status,variants,created_at,name,description,tags) VALUES($1,$2,'checkpoint','[]',$3,'Valid explicit name','','{}')`, uuid.Must(uuid.NewV7()), ex.ID, itNow)
	require.NoError(t, err, "empty Description and Tags remain valid authored values")
}
