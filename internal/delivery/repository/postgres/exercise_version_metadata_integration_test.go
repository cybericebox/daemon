package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestExerciseVersionMetadataFollowsPublicationAndRestore(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExerciseFull(t, repo, "Original metadata", "original description", []string{"web"}, itNow)
	first, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	require.Equal(t, ex.Name, first.Name)
	require.Equal(t, ex.Description, first.Description)
	require.Equal(t, ex.Tags, first.Tags)
	first, err = repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)

	identity := ex
	identity.Name, identity.Description, identity.Tags = "Unpublished rename", "new description", []string{"crypto"}
	identity.UpdatedAt = itNow.Add(2 * time.Minute)
	draft, err := repo.SaveDraftIdentity(ctx, identity, ex.UpdatedAt, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	require.Equal(t, first.Variants, draft.Variants, "metadata-only edit must preserve the published content")
	require.Equal(t, identity.Name, draft.Name)
	require.Equal(t, identity.Tags, draft.Tags)
	public, err := repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, ex.Name, public.Name, "public identity stays unchanged before Publish")
	require.Equal(t, ex.Tags, public.Tags)
	differs, err := repo.DraftDiffersFromPublished(ctx, ex.ID)
	require.NoError(t, err)
	require.True(t, differs, "metadata-only edits are unpublished changes")

	content := itMetadataVariants()
	content.AdminNote = "content saved after metadata PATCH"
	draft, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow.Add(3*time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	require.Equal(t, identity.Name, draft.Name, "the following ordinary content PUT must not revert the metadata PATCH")
	second, err := repo.Publish(ctx, ex.ID, itNow.Add(4*time.Minute))
	require.NoError(t, err)
	public, err = repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, identity.Name, public.Name)
	require.Equal(t, identity.Tags, public.Tags)
	old, err := repo.GetVersion(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, ex.Name, old.Name, "a pinned historical snapshot retains its own metadata")
	require.Equal(t, ex.Tags, old.Tags)

	restored, err := repo.CreateDraftFrom(ctx, ex.ID, first.ID, uuid.Must(uuid.NewV7()), itNow.Add(5*time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	require.Equal(t, ex.Name, restored.Name)
	require.Equal(t, ex.Tags, restored.Tags)
	_, err = repo.Discard(ctx, ex.ID)
	require.NoError(t, err)
	public, err = repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, second.Name, public.Name, "discard drops all draft content, never changes the published projection")
}

func TestExerciseMetadataPublishNameCollisionRollsBackWholeFamily(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Collision source")
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	published, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	other := mustCreateExercise(t, repo, "Collision occupied")
	ex.Name, ex.UpdatedAt = other.Name, itNow.Add(2*time.Minute)
	draft, err := repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err, "a draft rename must not overwrite another published task")
	_, err = repo.Publish(ctx, ex.ID, itNow.Add(3*time.Minute))
	require.Error(t, err)
	identity, err := repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, published.ID, identity.PublishedVersionID.UUID)
	require.Equal(t, draft.ID, identity.DraftVersionID.UUID)
	old, err := repo.GetVersion(ctx, published.ID)
	require.NoError(t, err)
	require.True(t, old.Status.IsPublished())
	stillDraft, err := repo.GetVersion(ctx, draft.ID)
	require.NoError(t, err)
	require.True(t, stillDraft.Status.IsDraft())
}

func itMetadataVariants() exerciseModel.ExerciseVersion {
	return exerciseModel.ExerciseVersion{Variants: itVariants()}
}

func TestExerciseMetadataCatalogProjectionUsesOneVisibleSnapshot(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "metaview")
	viewer := mustSeedUser(t, db, "metadata-manager@test.test")
	_, err := db.Queries.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: event.ID, UserID: viewer, Role: 1, CreatedAt: itNow})
	require.NoError(t, err)
	ex := mustCreateExerciseFull(t, repo, "Public old name", "public description", []string{"web"}, itNow)
	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	_, err = repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	ex.Name, ex.Description, ex.Tags, ex.UpdatedAt = "Draft new name", "private draft description", []string{"crypto"}, itNow.Add(2*time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	for _, tc := range []struct {
		name, search, tag, expected string
		visibility                  exerciseRepo.Visibility
		count                       int64
	}{
		{"working coherent", "Draft new", "crypto", "Draft new name", exerciseRepo.Visibility{}, 1},
		{"working old tags excluded", "Draft new", "web", "", exerciseRepo.Visibility{}, 0},
		{"public coherent", "Public old", "web", "Public old name", exerciseRepo.Visibility{ViewerID: uuid.NullUUID{UUID: viewer, Valid: true}}, 1},
		{"public draft excluded", "Draft new", "crypto", "", exerciseRepo.Visibility{ViewerID: uuid.NullUUID{UUID: viewer, Valid: true}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := exerciseRepo.PageParams{Search: tc.search, Tags: []string{tc.tag}, Visibility: tc.visibility, Limit: 10, SortBy: "name", SortDir: "asc"}
			rows, err := repo.ListPage(ctx, params)
			require.NoError(t, err)
			require.Len(t, rows, int(tc.count))
			count, err := repo.CountPage(ctx, params)
			require.NoError(t, err)
			require.Equal(t, tc.count, count)
			cursor, err := repo.ListCursor(ctx, exerciseRepo.ListParams{Search: tc.search, Tags: []string{tc.tag}, Visibility: tc.visibility, CursorCreatedAt: itCursorSentinelTime, CursorID: itMaxUUID, Limit: 10})
			require.NoError(t, err)
			require.Len(t, cursor, int(tc.count))
			count, err = repo.Count(ctx, tc.search, []string{tc.tag}, "exclude", tc.visibility)
			require.NoError(t, err)
			require.Equal(t, tc.count, count)
			if tc.count > 0 {
				require.Equal(t, tc.expected, rows[0].Name)
				require.Equal(t, tc.expected, cursor[0].Name)
			}
		})
	}
}

func TestEventExerciseMetadataStaysAtThePinnedVersion(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "metapin")
	ex := mustCreateExerciseFull(t, repo, "Pinned old name", "old description", []string{"web"}, itNow)
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	old, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	_, err = db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, ExerciseID: ex.ID, ExerciseVersionID: old.ID, Revision: 1, CreatedAt: itNow})
	require.NoError(t, err)
	owner := seedVPNUser(t, db, "metadata-pinned-lab@test.test")
	_, err = testDeployRepo.New(db.Queries).Create(ctx, exerciseModel.TestDeploy{
		ID: uuid.Must(uuid.NewV7()), GroupName: "metadata-pinned-lab", VersionID: old.ID, VariantID: old.Variants[0].ID,
		CreatedBy: owner, CreatedAt: itNow, ExpiresAt: itNow.Add(time.Hour),
	})
	require.NoError(t, err)
	ex.Name, ex.Tags, ex.UpdatedAt = "Published new name", []string{"crypto"}, itNow.Add(2*time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	_, err = repo.Publish(ctx, ex.ID, itNow.Add(3*time.Minute))
	require.NoError(t, err)
	rows, err := db.Queries.ListEventExerciseDetails(ctx, event.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Pinned old name", rows[0].ExerciseName)
	require.Equal(t, old.ID, rows[0].ExerciseVersionID)
	labs, err := db.Queries.ListPlatformTestLabs(ctx, postgres.ListPlatformTestLabsParams{Search: "Pinned old name", LimitVal: 10})
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.Equal(t, old.Name, labs[0].ExerciseName, "deployed lab search and label both use its version, not the newer publication")
}

func TestEventAnalyticsMetadataStaysAtItsPinnedVersion(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "metaanalytics")
	rtExec(t, db, `UPDATE event_challenges SET published = true, snapshot = '{"name":"Pinned task","difficulty":"easy"}'::jsonb WHERE id = $1`, f.challenge)
	var exerciseID uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT ee.exercise_id FROM event_exercises ee JOIN event_challenges ec ON ec.event_exercise_id = ee.id WHERE ec.id = $1`, f.challenge).Scan(&exerciseID))
	repo := exerciseRepo.New(db.Queries)
	identity, err := repo.GetByID(ctx, exerciseID)
	require.NoError(t, err)
	old, err := repo.GetVersion(ctx, identity.PublishedVersionID.UUID)
	require.NoError(t, err)
	identity.Name, identity.Tags, identity.UpdatedAt = "Analytics newer name", []string{"crypto"}, itNow.Add(time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, identity, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	_, err = repo.Publish(ctx, identity.ID, itNow.Add(2*time.Minute))
	require.NoError(t, err)
	from, to := anStart.Add(-time.Hour), anStart.Add(time.Hour)
	rows, err := db.Queries.ListPlatformAnalyticsTaskCatalog(ctx, postgres.ListPlatformAnalyticsTaskCatalogParams{FromAt: from, ToAt: to, RowLimit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, old.Name, rows[0].ExerciseName)
	require.Equal(t, old.Tags, rows[0].Tags)
	categories, err := db.Queries.ListPlatformAnalyticsTaskCategories(ctx, postgres.ListPlatformAnalyticsTaskCategoriesParams{FromAt: from, ToAt: to})
	require.NoError(t, err)
	require.Equal(t, old.Tags, categories, "historical event category options must not drift to the latest catalog tags")
}

func TestForkSourceMetadataStaysAtItsRecordedVersion(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "metafork")
	source := mustCreateExerciseFull(t, repo, "Fork source old name", "old description", []string{"web"}, itNow)
	_, err := repo.UpsertDraft(ctx, source.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	old, err := repo.Publish(ctx, source.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	fork, err := exerciseModel.NewFork(source, old.ID, event.ID, uuid.Nil, itNow)
	require.NoError(t, err)
	fork, err = repo.Create(ctx, fork)
	require.NoError(t, err)
	source.Name, source.UpdatedAt = "Fork source newer name", itNow.Add(2*time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, source, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	_, err = repo.Publish(ctx, source.ID, itNow.Add(3*time.Minute))
	require.NoError(t, err)
	extras, err := repo.CardExtras(ctx, []uuid.UUID{fork.ID})
	require.NoError(t, err)
	require.Equal(t, old.Name, extras[fork.ID].ForkedFromName, "source attribution describes the recorded fork snapshot")
}

func TestPolicySaveCannotRevertAConcurrentPublishedMetadataProjection(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Policy old name")
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	_, err = repo.Publish(ctx, ex.ID, itNow)
	require.NoError(t, err)
	ex.Name, ex.Tags, ex.UpdatedAt = "Policy newly published name", []string{"crypto"}, itNow.Add(time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	stalePolicy, err := repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	_, err = repo.Publish(ctx, ex.ID, itNow.Add(2*time.Minute))
	require.NoError(t, err)
	expected := stalePolicy.UpdatedAt
	require.NoError(t, stalePolicy.SetAccess(exerciseModel.AccessNone, false, uuid.Nil, itNow.Add(3*time.Minute)))
	updated, err := repo.Update(ctx, stalePolicy, expected)
	require.NoError(t, err)
	require.EqualValues(t, 1, updated)
	current, err := repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, ex.Name, current.Name, "ACL/archive writes cannot overwrite a newer SQL-owned published projection")
	require.Equal(t, ex.Tags, current.Tags)
	require.Equal(t, exerciseModel.AccessNone, current.AccessLevel)
}

func TestConcurrentFirstContentDraftAndMetadataPatchMergeBoth(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Concurrent first draft")
	first, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer first.Rollback(context.Background())
	content := itMetadataVariants()
	content.AdminNote = "content from first writer"
	_, err = exerciseRepo.New(db.Queries.WithTx(first)).UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{})
	require.NoError(t, err)
	second, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer second.Rollback(context.Background())
	pid := second.Conn().PgConn().PID()
	metadata := ex
	metadata.Name, metadata.Tags, metadata.UpdatedAt = "Metadata from second writer", []string{"crypto"}, itNow.Add(time.Minute)
	done := make(chan error, 1)
	go func() {
		_, saveErr := exerciseRepo.New(db.Queries.WithTx(second)).SaveDraftIdentity(ctx, metadata, ex.UpdatedAt, uuid.Must(uuid.NewV7()))
		if saveErr == nil {
			saveErr = second.Commit(ctx)
		}
		done <- saveErr
	}()
	// Observe the actual lock wait before releasing writer one, rather than a timing-only sleep.
	require.Eventually(t, func() bool {
		var waiting bool
		return db.Pool.QueryRow(ctx, `SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting) == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, first.Commit(ctx))
	require.NoError(t, <-done, "the partial-unique draft slot must merge the concurrent metadata edit")
	draft, err := repo.GetDraft(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, metadata.Name, draft.Name)
	require.Equal(t, content.AdminNote, draft.AdminNote)
	expectedJSON, err := json.Marshal(content.Variants)
	require.NoError(t, err)
	actualJSON, err := json.Marshal(draft.Variants)
	require.NoError(t, err)
	require.JSONEq(t, string(expectedJSON), string(actualJSON))
	var drafts int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM exercise_versions WHERE exercise_id = $1 AND status = 'draft'`, ex.ID).Scan(&drafts))
	require.Equal(t, 1, drafts)
}

func TestMetadataOnlyDraftCopiesPublishedFileReferences(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Metadata attachment clone")
	file, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: "attachment.txt", ContentType: "text/plain", SizeBytes: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: itNow})
	require.NoError(t, err)
	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	published, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: published.ID, FileIds: []uuid.UUID{file.ID}}))
	ex.Name, ex.UpdatedAt = "Metadata clone renamed", itNow.Add(2*time.Minute)
	draft, err := repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	refs, err := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: draft.ID})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file.ID}, refs)
}

func TestMetadataCheckpointRestorePreservesTheDisplacedFullDraft(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Snapshot baseline")
	ex.Name, ex.Tags, ex.UpdatedAt = "First working name", []string{"web"}, itNow.Add(time.Minute)
	draft, err := repo.SaveDraftIdentity(ctx, ex, itNow, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	checkpoint, err := repo.CreateCheckpoint(ctx, ex.ID, uuid.Must(uuid.NewV7()), "explicit snapshot", itNow.Add(2*time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	require.Equal(t, draft.Name, checkpoint.Name)
	ex.Name, ex.Tags, ex.UpdatedAt = "Second working name", []string{"crypto"}, itNow.Add(3*time.Minute)
	_, err = repo.SaveDraftIdentity(ctx, ex, itNow.Add(time.Minute), uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	preservedID := uuid.Must(uuid.NewV7())
	restored, err := repo.RestoreVersionPreservingDraft(ctx, ex.ID, checkpoint.ID, preservedID, itNow.Add(4*time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	require.Equal(t, "First working name", restored.Name)
	require.Equal(t, []string{"web"}, restored.Tags)
	preserved, err := repo.GetVersion(ctx, preservedID)
	require.NoError(t, err)
	require.Equal(t, "Second working name", preserved.Name)
	require.Equal(t, []string{"crypto"}, preserved.Tags)
	require.True(t, preserved.Status.IsCheckpoint())
}

func TestRestorePreservesTheAcknowledgedConcurrentMetadataAndContent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExerciseFull(t, repo, "Before concurrent restore", "original description", []string{"web"}, itNow)
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	source, err := repo.CreateCheckpoint(ctx, ex.ID, uuid.Must(uuid.NewV7()), "restore source", itNow, uuid.NullUUID{})
	require.NoError(t, err)
	first, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer first.Rollback(context.Background())
	metadata := ex
	metadata.Name, metadata.Description, metadata.Tags, metadata.UpdatedAt = "Latest acknowledged rename", "latest acknowledged description", []string{"crypto"}, itNow.Add(time.Minute)
	writer := exerciseRepo.New(db.Queries.WithTx(first))
	_, err = writer.SaveDraftIdentity(ctx, metadata, ex.UpdatedAt, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	content := itMetadataVariants()
	content.AdminNote, content.Variants[0].Note = "latest content note", "latest variant note"
	latest, err := writer.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow.Add(time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	second, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer second.Rollback(context.Background())
	pid, preservedID := second.Conn().PgConn().PID(), uuid.Must(uuid.NewV7())
	done := make(chan error, 1)
	go func() {
		_, restoreErr := exerciseRepo.New(db.Queries.WithTx(second)).RestoreVersionPreservingDraft(ctx, ex.ID, source.ID, preservedID, itNow.Add(2*time.Minute), uuid.NullUUID{})
		if restoreErr == nil {
			restoreErr = second.Commit(ctx)
		}
		done <- restoreErr
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		return db.Pool.QueryRow(ctx, `SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting) == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, first.Commit(ctx))
	require.NoError(t, <-done)
	preserved, err := repo.GetVersion(ctx, preservedID)
	require.NoError(t, err)
	require.Equal(t, latest.Name, preserved.Name)
	require.Equal(t, latest.Description, preserved.Description)
	require.Equal(t, latest.Tags, preserved.Tags)
	require.Equal(t, latest.AdminNote, preserved.AdminNote)
	require.Equal(t, latest.Variants, preserved.Variants, "the safety checkpoint must preserve the same acknowledged content and metadata")
	restored, err := repo.GetDraft(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, source.Name, restored.Name)
	require.Equal(t, source.Description, restored.Description)
	require.Equal(t, source.Tags, restored.Tags)
	require.Equal(t, source.Variants, restored.Variants)
}

func TestVersionMetadataMigrationBackfillsKnownBaselineWithoutRewritingHistory(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Known metadata baseline")
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), itMetadataVariants(), itNow, uuid.NullUUID{})
	require.NoError(t, err)
	published, err := repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	checkpoint, err := repo.CreateCheckpoint(ctx, ex.ID, uuid.Must(uuid.NewV7()), "preserved label", itNow.Add(2*time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	down, err := os.ReadFile("migrations/0195_exercise_version_metadata.down.sql")
	require.NoError(t, err)
	up, err := os.ReadFile("migrations/0195_exercise_version_metadata.up.sql")
	require.NoError(t, err)
	// Undo/reapply dependent migrations in order, as the migration runner does.
	down196, err := os.ReadFile("migrations/0196_exercise_import_attempts.down.sql")
	require.NoError(t, err)
	up196, err := os.ReadFile("migrations/0196_exercise_import_attempts.up.sql")
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(down196))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(down))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(up))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(up196))
	require.NoError(t, err)
	versions, err := repo.ListVersions(ctx, ex.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	for _, v := range versions {
		require.Equal(t, ex.Name, v.Name)
		require.Equal(t, ex.Description, v.Description)
		require.Equal(t, ex.Tags, v.Tags)
		if v.ID == checkpoint.ID {
			require.True(t, v.Status.IsCheckpoint())
			require.Equal(t, checkpoint.Label, v.Label)
		} else {
			require.Equal(t, published.ID, v.ID)
			require.True(t, v.Status.IsPublished())
		}
	}
	identity, err := repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, published.ID, identity.PublishedVersionID.UUID)
}

func TestVisibleVariantResourcesFollowTheSelectedSnapshot(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Visible snapshot resources")
	content := itMetadataVariants()
	content.Variants[0].Topology.Devices = []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, ResourcePreset: "medium"}}
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, uuid.NullUUID{})
	require.NoError(t, err)
	_, err = repo.Publish(ctx, ex.ID, itNow.Add(time.Minute))
	require.NoError(t, err)
	content.Variants[0].Topology.Devices[0].ResourcePreset = "small"
	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow.Add(2*time.Minute), uuid.NullUUID{})
	require.NoError(t, err)
	working, err := repo.VisibleVariants(ctx, []uuid.UUID{ex.ID}, uuid.NullUUID{})
	require.NoError(t, err)
	require.Equal(t, "small", working[ex.ID][0].Topology.Devices[0].ResourcePreset)
	public, err := repo.VisibleVariants(ctx, []uuid.UUID{ex.ID}, uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true})
	require.NoError(t, err)
	require.Equal(t, "medium", public[ex.ID][0].Topology.Devices[0].ResourcePreset)
}
