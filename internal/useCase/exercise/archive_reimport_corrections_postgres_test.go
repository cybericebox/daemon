package exercise_test

import (
	"bytes"
	"context"
	"github.com/cybericebox/daemon/internal/config"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaUseCase "github.com/cybericebox/daemon/internal/useCase/media"
	"sync"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func correctionTarget(t *testing.T) (*testhelpers.TestDB, context.Context, userModel.User, exerciseModel.Exercise) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "correction@test.test", importJobT0)
	user.Role = rbac.RoleSuperAdmin
	_, err := userRepo.New(db.Queries).Create(ctx, user)
	require.NoError(t, err)
	e, err := exerciseModel.NewExercise("Existing correction target", "", nil, user.ID, importJobT0)
	require.NoError(t, err)
	e, err = exerciseRepo.New(db.Queries).Create(ctx, e)
	require.NoError(t, err)
	return db, ctx, user, e
}

type draftReferencePause struct {
	once             sync.Once
	reached, release chan struct{}
}

func (p *draftReferencePause) wait(ctx context.Context) error {
	var err error
	p.once.Do(func() {
		close(p.reached)
		select {
		case <-p.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

type pausedRestoreMedia struct {
	*fakeMedia
	q     *postgres.Queries
	pause *draftReferencePause
}

func (m pausedRestoreMedia) ReplaceReferences(ctx context.Context, kind string, id uuid.UUID, ids []uuid.UUID) error {
	row, err := m.q.GetExerciseVersionByID(ctx, id)
	if err != nil {
		return err
	}
	if row.Status == "draft" {
		if err = m.pause.wait(ctx); err != nil {
			return err
		}
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return m.q.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: kind, RefID: id, FileIds: ids})
}

type pausedRestoreQueries struct {
	exercise.CatalogRepository
	pause *draftReferencePause
}

func (q pausedRestoreQueries) ReplaceFileReferences(ctx context.Context, in postgres.ReplaceFileReferencesParams) error {
	row, err := q.GetExerciseVersionByID(ctx, in.RefID)
	if err != nil {
		return err
	}
	if row.Status == "draft" {
		if err = q.pause.wait(ctx); err != nil {
			return err
		}
	}
	return q.CatalogRepository.ReplaceFileReferences(ctx, in)
}

type pausedRestoreWorker struct {
	postgres.IUnitOfWorker[exercise.CatalogRepository]
	pause *draftReferencePause
}

func (w pausedRestoreWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	ctx, q, u, err := w.IUnitOfWorker.UnitOfWork(ctx)
	return ctx, pausedRestoreQueries{q, w.pause}, u, err
}

// Exercise the actual lifecycle writers and importer asynchronously: a correct
// restore holds the parent while the importer waits, then refuses its stale
// preview. The old detached reference write instead resumes after the import.
func TestRestoreAndRollbackSerializeContentAndReferencesWithReimport(t *testing.T) {
	for _, mode := range []string{"restore-existing", "restore-new", "rollback-new"} {
		t.Run(mode, func(t *testing.T) {
			db, ctx, user, e := correctionTarget(t)
			repo := exerciseRepo.New(db.Queries)
			oldFile, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: "old.txt", ContentType: "text/plain", SizeBytes: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: importJobT0, CreatedBy: uuid.NullUUID{UUID: user.ID, Valid: true}})
			require.NoError(t, err)
			published, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "published", Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Name: "published", Attachments: []exerciseModel.AttachmentRef{{FileID: oldFile.ID, Name: "old.txt"}}}}}}}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			published, err = repo.Publish(ctx, e.ID, importJobT0)
			require.NoError(t, err)
			require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: published.ID, FileIds: []uuid.UUID{oldFile.ID}}))
			event, err := eventModel.NewEvent("restorepin", "Pinned restore regression", importJobT0, importJobT0.Add(time.Hour), user.ID, importJobT0)
			require.NoError(t, err)
			event, err = eventRepo.New(db.Queries).Create(ctx, event)
			require.NoError(t, err)
			pin := uuid.Must(uuid.NewV7())
			_, err = db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{ID: pin, EventID: event.ID, ExerciseID: e.ID, ExerciseVersionID: published.ID, Revision: 1, CreatedAt: importJobT0})
			require.NoError(t, err)
			if mode == "restore-existing" {
				draft, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "acknowledged work", Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Name: "old", Attachments: []exerciseModel.AttachmentRef{{FileID: oldFile.ID, Name: "old.txt"}}}}}}}, importJobT0, uuid.NullUUID{})
				require.NoError(t, err)
				require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: draft.ID, FileIds: []uuid.UUID{oldFile.ID}}))
			}
			pause := &draftReferencePause{reached: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(pause.release) })
			base := postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))
			restorer := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: pausedRestoreMedia{newFakeMedia(), db.Queries, pause}, CatalogUoW: pausedRestoreWorker{base, pause}})
			restoreDone := make(chan error, 1)
			go func() {
				var err error
				if mode == "rollback-new" {
					_, err = restorer.RollbackToVersion(ctx, e.ID, published.ID, user.ID)
				} else {
					_, err = restorer.RestoreToVersion(ctx, e.ID, published.ID, user.ID)
				}
				restoreDone <- err
			}()
			select {
			case <-pause.reached:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Imported attachment target"},"versions":[{"status":"draft","admin_note":"imported","variants":[{"tasks":[{"name":"Subtask","attachments":[{"file":"files/new.txt"}]}]}]}]}`), "files/new.txt": []byte("new unique content")})
			require.NoError(t, err)
			sources := newPostgresSources(t, db)
			sources.stage(archiveUpID, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
			importer := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: postgresArchiveMedia{newFakeMedia(), db.Queries}, CatalogUoW: base})
			importer.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
			actor := exercise.Actor{UserID: user.ID, Role: user.Role}
			in := exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: e.ID}}}
			preview, err := importer.PreviewArchiveImport(ctx, actor, in)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				_, err := importer.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}})
				done <- err
			}()
			var importErr error
			completed := false
			// Wait for either the faulty completed import or a genuine PostgreSQL lock
			// wait. Never release the pause merely because a scheduler timer elapsed.
			for {
				select {
				case importErr = <-done:
					completed = true
				default:
				}
				if completed {
					break
				}
				var blocked bool
				require.NoError(t, db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&blocked))
				if blocked {
					break
				}
				select {
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			release.Do(func() { close(pause.release) })
			require.NoError(t, <-restoreDone)
			if !completed {
				importErr = <-done
			}
			if importErr != nil {
				require.ErrorIs(t, importErr, exerciseModel.ErrImportConfirmationStale.Err())
			}
			current, err := repo.GetDraft(ctx, e.ID)
			require.NoError(t, err)
			refs, err := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: current.ID})
			require.NoError(t, err)
			require.ElementsMatch(t, exerciseModel.CollectFileIDs(current.Variants), refs, "a completed import must never lose its references to a late restore")
			if mode == "restore-existing" {
				found := false
				for _, v := range mustVersions(t, repo, ctx, e.ID) {
					if v.AdminNote == "acknowledged work" {
						found = true
						r, err := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: v.ID})
						require.NoError(t, err)
						require.Equal(t, []uuid.UUID{oldFile.ID}, r)
					}
				}
				require.True(t, found, "restore preserves the acknowledged draft as a referenced checkpoint")
			}
			old, err := repo.GetVersion(ctx, published.ID)
			require.NoError(t, err)
			require.Equal(t, "published", old.AdminNote)
			identity, err := repo.GetByID(ctx, e.ID)
			require.NoError(t, err)
			require.Equal(t, published.ID, identity.PublishedVersionID.UUID)
			pubRefs, err := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: published.ID})
			require.NoError(t, err)
			require.Equal(t, []uuid.UUID{oldFile.ID}, pubRefs)
			var pinned uuid.UUID
			require.NoError(t, db.Pool.QueryRow(ctx, `SELECT exercise_version_id FROM event_exercises WHERE id=$1`, pin).Scan(&pinned))
			require.Equal(t, published.ID, pinned)

			// Fresh confirmation after serialization remains usable.
			sources.stage(archiveUpID, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
			preview, err = importer.PreviewArchiveImport(ctx, actor, in)
			require.NoError(t, err)
			_, err = importer.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}})
			require.NoError(t, err)
			current, err = repo.GetDraft(ctx, e.ID)
			require.NoError(t, err)
			refs, err = db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: current.ID})
			require.NoError(t, err)
			require.ElementsMatch(t, exerciseModel.CollectFileIDs(current.Variants), refs)
		})
	}
}

func correctionMedia(db *testhelpers.TestDB) *mediaUseCase.MediaUseCase {
	return mediaUseCase.NewMediaUseCase(mediaUseCase.Dependencies{Repo: db.Queries, Storage: testhelpers.NewMemoryStorage(), Config: config.MediaConfig{MaxUploadBytes: 64 << 20, UploadChunkBytes: 64 << 20, UploadTTL: time.Hour}})
}
func correctionSource(t *testing.T, ctx context.Context, media *mediaUseCase.MediaUseCase, by uuid.UUID, body []byte, file bool) exercise.PreviewArchiveInput {
	t.Helper()
	if file {
		f, err := media.UploadFile(ctx, "source.zip", "application/zip", bytes.NewReader(body), by)
		require.NoError(t, err)
		return exercise.PreviewArchiveInput{FileID: f.ID}
	}
	up, err := media.StartUpload(ctx, "source.zip", "application/zip", int64(len(body)), by, mediaModel.UploadPurposeExerciseArchive)
	require.NoError(t, err)
	_, err = media.PutChunk(ctx, up.ID, by, 0, bytes.NewReader(body))
	require.NoError(t, err)
	return exercise.PreviewArchiveInput{UploadID: up.ID}
}

func TestReimportRechecksRealSourceAfterTargetLockWait(t *testing.T) {
	cases := []struct {
		name         string
		file, worker bool
		mutation     string
	}{
		{"upload-delete", false, false, "delete"}, {"upload-expire", false, false, "expire"}, {"upload-purpose", false, false, "purpose"}, {"upload-incomplete", false, false, "incomplete"}, {"upload-owner", false, false, "owner"},
		{"file-delete", true, false, "delete"}, {"file-owner", true, false, "owner"},
		{"worker-upload-delete", false, true, "delete"}, {"worker-upload-expire", false, true, "expire"}, {"worker-file-delete", true, true, "delete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, ctx, user, e := correctionTarget(t)
			repo := exerciseRepo.New(db.Queries)
			draft, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "unchanged"}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			entries := map[string][]byte{"a/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Forbidden replacement"},"versions":[{"status":"draft","variants":[]}]}`), "b/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Must not create sibling"},"versions":[{"status":"draft","variants":[]}]}`)}
			if c.worker {
				entries["a/exercise.json"] = imageTask("Forbidden replacement", [2]string{"web", "images/web.tar"})
				entries["a/images/web.tar"] = webImageTar
			}
			archive, err := exercise.BuildArchiveV1(entries)
			require.NoError(t, err)
			media := correctionMedia(db)
			in := correctionSource(t, ctx, media, user.ID, archive, c.file)
			in.Targets = []exercise.ImportTarget{{ItemKey: "a", Mode: "update", ExerciseID: e.ID}, {ItemKey: "b", Mode: "new"}}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: media, CatalogUoW: postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))})
			var args jobsModel.ExerciseImportArgs
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: media, Images: &fakeImages{}, Queue: func(_ context.Context, a jobsModel.ExerciseImportArgs) error { args = a; return nil }})
			actor := exercise.Actor{UserID: user.ID, Role: user.Role}
			preview, err := uc.PreviewArchiveImport(ctx, actor, in)
			require.NoError(t, err)
			request := exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: "a", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}}
			var jobID uuid.UUID
			if c.worker {
				start, err := uc.CommitArchiveImport(ctx, actor, request)
				require.NoError(t, err)
				jobID = start.JobID
				require.NotEqual(t, uuid.Nil, jobID)
			}
			blocker, err := db.Pool.Begin(ctx)
			require.NoError(t, err)
			defer blocker.Rollback(context.Background())
			_, err = blocker.Exec(ctx, `SELECT id FROM exercises WHERE id=$1 FOR UPDATE`, e.ID)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				if c.worker {
					done <- uc.RunArchiveImport(ctx, args)
				} else {
					_, err := uc.CommitArchiveImport(ctx, actor, request)
					done <- err
				}
			}()
			for {
				var blocked bool
				require.NoError(t, db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&blocked))
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("import did not reach target lock: %v", err)
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			table, id := "media_uploads", in.UploadID
			if c.file {
				table, id = "files", in.FileID
			}
			switch c.mutation {
			case "delete":
				_, err = db.Pool.Exec(ctx, `DELETE FROM `+table+` WHERE id=$1`, id)
			case "expire":
				_, err = db.Pool.Exec(ctx, `UPDATE media_uploads SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id)
			case "purpose":
				_, err = db.Pool.Exec(ctx, `UPDATE media_uploads SET purpose='file' WHERE id=$1`, id)
			case "incomplete":
				_, err = db.Pool.Exec(ctx, `UPDATE media_uploads SET chunks_received=0 WHERE id=$1`, id)
			case "owner":
				other := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "other@test.test", importJobT0)
				_, err = userRepo.New(db.Queries).Create(ctx, other)
				require.NoError(t, err)
				_, err = db.Pool.Exec(ctx, `UPDATE `+table+` SET created_by=$2 WHERE id=$1`, id, other.ID)
			}
			require.NoError(t, err)
			require.NoError(t, blocker.Commit(ctx))
			err = <-done
			if c.worker {
				require.NoError(t, err)
				job, err := uc.ArchiveImportJob(ctx, jobID, user.ID)
				require.NoError(t, err)
				require.Equal(t, exerciseModel.ImportJobFailed, job.State)
				require.Empty(t, job.Results)
			} else {
				require.Error(t, err, "an ineligible source after staging must refuse every task write")
				if c.file {
					require.ErrorIs(t, err, mediaModel.ErrFileNotFound.Err())
				} else if c.mutation == "purpose" || c.mutation == "incomplete" {
					require.ErrorIs(t, err, exerciseModel.ErrArchiveUploadNotReady.Err())
				} else {
					require.ErrorIs(t, err, mediaModel.ErrUploadNotFound.Err())
				}

			}
			current, err := repo.GetDraft(ctx, e.ID)
			require.NoError(t, err)
			require.Equal(t, draft, current)
			var count int
			require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM exercises`).Scan(&count))
			require.Equal(t, 1, count)
			refs, err := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: draft.ID})
			require.NoError(t, err)
			require.Empty(t, refs)
		})
	}
}

// Existing fake-source archive tests retain injected bytes, but the final
// transactional source check always reads an actual complete media_uploads row.
func newPostgresSources(t *testing.T, db *testhelpers.TestDB) *fakeSources {
	t.Helper()
	s := newFakeSources()
	s.persist = func(up mediaModel.Upload) {
		_, err := db.Pool.Exec(context.Background(), `INSERT INTO media_uploads(id,created_by,name,content_type,size_bytes,chunk_bytes,chunks_received,created_at,expires_at,purpose) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET expires_at=excluded.expires_at,chunks_received=excluded.chunks_received`, up.ID, up.CreatedBy, up.Name, up.ContentType, up.SizeBytes, up.ChunkBytes, up.ChunksReceived, up.CreatedAt, up.ExpiresAt, string(up.Purpose))
		require.NoError(t, err)
	}
	return s
}

type lockedSourceQueries struct {
	exercise.CatalogRepository
	pause *draftReferencePause
}

func (q lockedSourceQueries) LockExerciseImportUpload(ctx context.Context, id uuid.UUID) (postgres.MediaUpload, error) {
	row, err := q.CatalogRepository.LockExerciseImportUpload(ctx, id)
	if err == nil {
		err = q.pause.wait(ctx)
	}
	return row, err
}
func (q lockedSourceQueries) LockExerciseImportFile(ctx context.Context, id uuid.UUID) (postgres.File, error) {
	row, err := q.CatalogRepository.LockExerciseImportFile(ctx, id)
	if err == nil {
		err = q.pause.wait(ctx)
	}
	return row, err
}

type lockedSourceWorker struct {
	postgres.IUnitOfWorker[exercise.CatalogRepository]
	pause *draftReferencePause
}

func (w lockedSourceWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	ctx, q, u, err := w.IUnitOfWorker.UnitOfWork(ctx)
	return ctx, lockedSourceQueries{q, w.pause}, u, err
}

func TestReimportSourceLockStabilizesDeletionUntilTaskCommit(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "upload", true: "file"}[file], func(t *testing.T) {
			db, ctx, user, e := correctionTarget(t)
			repo := exerciseRepo.New(db.Queries)
			_, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "before"}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			archive := mustExerciseArchive(t, `{"name":"Committed with stabilized source"}`, `[{"status":"draft","variants":[]}]`)
			media := correctionMedia(db)
			in := correctionSource(t, ctx, media, user.ID, archive, file)
			in.Targets = []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: e.ID}}
			pause := &draftReferencePause{reached: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(pause.release) })
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: media, CatalogUoW: lockedSourceWorker{postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool)), pause}})
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: media})
			actor := exercise.Actor{UserID: user.ID, Role: user.Role}
			preview, err := uc.PreviewArchiveImport(ctx, actor, in)
			require.NoError(t, err)
			committed := make(chan error, 1)
			go func() {
				_, err := uc.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}})
				committed <- err
			}()
			select {
			case <-pause.reached:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			table, id := "media_uploads", in.UploadID
			if file {
				table, id = "files", in.FileID
			}
			deleted := make(chan error, 1)
			go func() { _, err := db.Pool.Exec(ctx, `DELETE FROM `+table+` WHERE id=$1`, id); deleted <- err }()
			for {
				var blocked bool
				require.NoError(t, db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&blocked))
				if blocked {
					break
				}
				select {
				case err := <-deleted:
					t.Fatalf("source deletion escaped transaction lock: %v", err)
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			release.Do(func() { close(pause.release) })
			require.NoError(t, <-committed)
			require.NoError(t, <-deleted)
			current, err := repo.GetDraft(ctx, e.ID)
			require.NoError(t, err)
			require.Equal(t, "Committed with stabilized source", current.Name)
		})
	}
}
