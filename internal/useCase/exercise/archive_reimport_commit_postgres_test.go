package exercise_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type postgresArchiveMedia struct {
	*fakeMedia
	q *postgres.Queries
}

func (m postgresArchiveMedia) UploadFile(ctx context.Context, name, kind string, r io.Reader, by uuid.UUID) (mediaModel.File, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return mediaModel.File{}, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	row, err := m.q.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: name, ContentType: kind, SizeBytes: int64(len(body)), ContentHash: hash, CreatedAt: time.Now(), CreatedBy: uuid.NullUUID{UUID: by, Valid: true}})
	return mediaModel.File{ID: row.ID, Name: row.Name, ContentType: row.ContentType, ContentHash: row.ContentHash, CreatedBy: row.CreatedBy}, err
}

type commitFailureQueries struct {
	exercise.CatalogRepository
	calls int
}

func (q *commitFailureQueries) ReplaceFileReferences(ctx context.Context, in postgres.ReplaceFileReferencesParams) error {
	q.calls++
	if q.calls == 2 {
		return errors.New("last reference write failed")
	}
	return q.CatalogRepository.ReplaceFileReferences(ctx, in)
}

type commitFailureWorker struct {
	postgres.IUnitOfWorker[exercise.CatalogRepository]
}

func (w commitFailureWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	ctx, q, u, err := w.IUnitOfWorker.UnitOfWork(ctx)
	return ctx, &commitFailureQueries{CatalogRepository: q}, u, err
}

func TestSourceReimportAtomicallyUpdatesDraftAndCreatesNewFamily(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "last-write-rollback"}[failLast], func(t *testing.T) {
			db := testhelpers.SetupTestDB(t)
			ctx := context.Background()
			user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "atomic-import@test.test", importJobT0)
			user.Role = rbac.RoleSuperAdmin
			_, err := userRepo.New(db.Queries).Create(ctx, user)
			require.NoError(t, err)
			repo := exerciseRepo.New(db.Queries)
			entity, err := exerciseModel.NewExercise("Existing public task", "public description", []string{"web"}, user.ID, importJobT0)
			require.NoError(t, err)
			entity, err = repo.Create(ctx, entity)
			require.NoError(t, err)
			published, err := repo.UpsertDraft(ctx, entity.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "published old"}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			published, err = repo.Publish(ctx, entity.ID, importJobT0)
			require.NoError(t, err)
			draft, err := repo.UpsertDraft(ctx, entity.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "acknowledged old draft"}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			oldFile, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: "pinned.txt", ContentType: "text/plain", SizeBytes: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: importJobT0, CreatedBy: uuid.NullUUID{UUID: user.ID, Valid: true}})
			require.NoError(t, err)
			for _, version := range []uuid.UUID{published.ID, draft.ID} {
				require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: version, FileIds: []uuid.UUID{oldFile.ID}}))
			}
			event, err := eventModel.NewEvent("reimportpin", "Pinned import regression", importJobT0, importJobT0.Add(24*time.Hour), user.ID, importJobT0)
			require.NoError(t, err)
			event, err = eventRepo.New(db.Queries).Create(ctx, event)
			require.NoError(t, err)
			pin := uuid.Must(uuid.NewV7())
			_, err = db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{ID: pin, EventID: event.ID, ExerciseID: entity.ID, ExerciseVersionID: published.ID, Revision: 1, CreatedAt: importJobT0})
			require.NoError(t, err)
			archive, err := exercise.BuildArchiveV1(map[string][]byte{
				"a/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Imported working rename","description":"new authored description","tags":["crypto"]},"versions":[{"status":"draft","admin_note":"imported draft","variants":[{"tasks":[{"name":"Imported subtask","attachments":[{"file":"files/new.txt"}]}]}]}]}`),
				"a/files/new.txt": []byte("imported file content"),
				"b/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"New sibling task"},"versions":[{"status":"draft","variants":[]}]}`),
			})
			require.NoError(t, err)
			sources := newPostgresSources(t, db)
			sources.stage(archiveUpID, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
			var worker postgres.IUnitOfWorker[exercise.CatalogRepository] = postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))
			if failLast {
				worker = commitFailureWorker{worker}
			}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: postgresArchiveMedia{newFakeMedia(), db.Queries}, CatalogUoW: worker})
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
			actor := exercise.Actor{UserID: user.ID, Role: rbac.RoleSuperAdmin}
			in := exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: "a", Mode: "update", ExerciseID: entity.ID}, {ItemKey: "b", Mode: "new"}}}
			preview, err := uc.PreviewArchiveImport(ctx, actor, in)
			require.NoError(t, err)
			result, err := uc.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: "a", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}})
			if failLast {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, result.Created)
				require.Equal(t, 1, result.Updated)
				require.Len(t, result.Results, 2)
			}
			identity, loadErr := repo.GetByID(ctx, entity.ID)
			require.NoError(t, loadErr)
			require.Equal(t, entity.Name, identity.Name, "import does not publish authored metadata")
			require.Equal(t, published.ID, identity.PublishedVersionID.UUID)
			current, loadErr := repo.GetDraft(ctx, entity.ID)
			require.NoError(t, loadErr)
			require.Equal(t, draft.ID, current.ID, "replace the same draft slot")
			var taskCount int
			require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM exercises`).Scan(&taskCount))
			if failLast {
				require.Equal(t, "acknowledged old draft", current.AdminNote)
				require.Equal(t, 2, len(mustVersions(t, repo, ctx, entity.ID)))
				require.Equal(t, 1, taskCount)
			} else {
				require.Equal(t, "Imported working rename", current.Name)
				require.Equal(t, []string{"crypto"}, current.Tags)
				require.Equal(t, 2, taskCount)
				_, err = uc.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: "a", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}})
				require.Error(t, err, "the prior confirmation cannot replace the changed draft again")
			}
			old, loadErr := repo.GetVersion(ctx, published.ID)
			require.NoError(t, loadErr)
			require.Equal(t, "published old", old.AdminNote)
			require.Equal(t, entity.Name, old.Name)
			refs, loadErr := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: published.ID})
			require.NoError(t, loadErr)
			require.Equal(t, []uuid.UUID{oldFile.ID}, refs, "pinned publication references remain exact")
			var pinned uuid.UUID
			require.NoError(t, db.Pool.QueryRow(ctx, `SELECT exercise_version_id FROM event_exercises WHERE id=$1`, pin).Scan(&pinned))
			require.Equal(t, published.ID, pinned)
			refs, loadErr = db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: draft.ID})
			require.NoError(t, loadErr)
			if failLast {
				require.Equal(t, []uuid.UUID{oldFile.ID}, refs)
			} else {
				require.Equal(t, exerciseModel.CollectFileIDs(current.Variants), refs)
				require.NotContains(t, refs, oldFile.ID)
			}
		})
	}
}

func mustVersions(t *testing.T, repo *exerciseRepo.Repository, ctx context.Context, id uuid.UUID) []exerciseModel.ExerciseVersion {
	t.Helper()
	v, err := repo.ListVersions(ctx, id)
	require.NoError(t, err)
	return v
}

type failDoneQueries struct{ exercise.CatalogRepository }

func (q failDoneQueries) UpdateExerciseImportJob(ctx context.Context, p postgres.UpdateExerciseImportJobParams) (int64, error) {
	if p.State == "done" {
		return 0, errors.New("injected durable outcome failure")
	}
	return q.CatalogRepository.UpdateExerciseImportJob(ctx, p)
}

type lostCommitUnit struct{ postgres.UoW }

func (u lostCommitUnit) Save() error {
	if err := u.UoW.Save(); err != nil {
		return err
	}
	return errors.New("commit acknowledgement lost")
}

type outcomeWorker struct {
	postgres.IUnitOfWorker[exercise.CatalogRepository]
	failDone, lostAck bool
}

func (w outcomeWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	ctx, q, u, err := w.IUnitOfWorker.UnitOfWork(ctx)
	if w.failDone {
		q = failDoneQueries{q}
	}
	if w.lostAck {
		u = lostCommitUnit{u}
	}
	return ctx, q, u, err
}

func testAtomicImportOutcome(t *testing.T) {
	for _, mode := range []string{"commit", "outcome-write-rolls-back", "lost-commit-ack"} {
		t.Run(mode, func(t *testing.T) {
			db := testhelpers.SetupTestDB(t)
			ctx := context.Background()
			user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "import-outcome@test.test", importJobT0)
			user.Role = rbac.RoleSuperAdmin
			_, err := userRepo.New(db.Queries).Create(ctx, user)
			require.NoError(t, err)
			sources := newPostgresSources(t, db)
			sources.stage(archiveUpID, user.ID, mustExerciseArchive(t, `{"name":"Atomic durable import"}`, `[{"status":"draft","variants":[]}]`), mediaModel.UploadPurposeExerciseArchive)
			worker := outcomeWorker{IUnitOfWorker: postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool)), failDone: mode == "outcome-write-rolls-back", lostAck: mode == "lost-commit-ack"}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: newFakeMedia(), CatalogUoW: worker})
			var args jobsModel.ExerciseImportArgs
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources, Images: &fakeImages{}, Queue: func(_ context.Context, in jobsModel.ExerciseImportArgs) error { args = in; return nil }})
			id, err := uc.QueueArchiveImport(ctx, exercise.QueueImportInput{Source: exerciseModel.ImportSourceUpload, SourceID: archiveUpID, CreatedBy: user.ID})
			require.NoError(t, err)
			args.Generation = 0 // an old already-queued all-NEW argument means migrated generation1.
			require.NoError(t, uc.RunArchiveImport(ctx, args))
			job, err := uc.ArchiveImportJob(ctx, id, user.ID)
			require.NoError(t, err)
			var count int
			require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM exercises`).Scan(&count))
			if mode == "outcome-write-rolls-back" {
				require.Equal(t, exerciseModel.ImportJobFailed, job.State)
				require.Zero(t, count)
				require.Empty(t, job.Results)
			} else {
				require.Equal(t, exerciseModel.ImportJobDone, job.State)
				require.Equal(t, 1, count)
				require.Len(t, job.Results, 1)
				require.Equal(t, 1, job.Created)
				require.Zero(t, job.Updated)
				require.NoError(t, uc.RunArchiveImport(ctx, args), "a replay must not reopen consumed source or create another task")
				require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM exercises`).Scan(&count))
				require.Equal(t, 1, count)
				_, err = uc.QueueArchiveImport(ctx, exercise.QueueImportInput{Source: exerciseModel.ImportSourceUpload, SourceID: archiveUpID, CreatedBy: user.ID, Options: exercise.ImportOptions{Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "new"}}}})
				require.ErrorIs(t, err, exerciseModel.ErrImportPlanConflict.Err())
			}
		})
	}
}

type beforeCatalogWorker struct {
	postgres.IUnitOfWorker[exercise.CatalogRepository]
	before func()
}

func (w beforeCatalogWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	w.before()
	return w.IUnitOfWorker.UnitOfWork(ctx)
}

func TestReimportRechecksPreviewAgainstConcurrentFamilyAndActorChanges(t *testing.T) {
	for _, change := range []string{"save", "first-draft", "publish", "discard", "archive", "delete", "role-revoked", "different-source"} {
		t.Run(change, func(t *testing.T) {
			db := testhelpers.SetupTestDB(t)
			ctx := context.Background()
			repo := exerciseRepo.New(db.Queries)
			user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "recheck@test.test", importJobT0)
			user.Role = rbac.RoleSuperAdmin
			_, err := userRepo.New(db.Queries).Create(ctx, user)
			require.NoError(t, err)
			e, err := exerciseModel.NewExercise("Concurrent target", "", nil, user.ID, importJobT0)
			require.NoError(t, err)
			e, err = repo.Create(ctx, e)
			require.NoError(t, err)
			_, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "published"}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			_, err = repo.Publish(ctx, e.ID, importJobT0)
			require.NoError(t, err)
			if change != "first-draft" {
				_, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "previewed draft"}, importJobT0, uuid.NullUUID{})
				require.NoError(t, err)
			}
			archive := mustExerciseArchive(t, `{"name":"Must never be applied"}`, `[{"status":"draft","variants":[]}]`)
			sources := newPostgresSources(t, db)
			sources.stage(archiveUpID, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
			var before []byte
			state := func() []byte {
				var result []byte
				require.NoError(t, db.Pool.QueryRow(ctx, `SELECT jsonb_build_object('e',(SELECT to_jsonb(e) FROM exercises e WHERE id=$1),'v',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM exercise_versions v WHERE exercise_id=$1))`, e.ID).Scan(&result))
				return result
			}
			worker := beforeCatalogWorker{IUnitOfWorker: postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool)), before: func() {
				var err error
				switch change {
				case "save", "first-draft":
					_, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "acknowledged concurrent save"}, importJobT0, uuid.NullUUID{})
				case "publish":
					_, err = repo.Publish(ctx, e.ID, importJobT0)
				case "discard":
					_, err = repo.Discard(ctx, e.ID)
				case "archive":
					_, err = db.Pool.Exec(ctx, `UPDATE exercises SET archived_at=now() WHERE id=$1`, e.ID)
				case "delete":
					_, err = repo.Delete(ctx, e.ID)
				case "role-revoked":
					_, err = db.Pool.Exec(ctx, `UPDATE users SET role='user' WHERE id=$1`, user.ID)
				}
				require.NoError(t, err)
				before = state()
			}}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: newFakeMedia(), CatalogUoW: worker})
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
			actor := exercise.Actor{UserID: user.ID, Role: rbac.RoleSuperAdmin}
			in := exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: e.ID}}}
			preview, err := uc.PreviewArchiveImport(ctx, actor, in)
			require.NoError(t, err)
			if change == "different-source" {
				id := uuid.Must(uuid.NewV7())
				sources.stage(id, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
				in.UploadID = id
				before = state()
			}
			_, err = uc.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}})
			require.Error(t, err)
			if change != "archive" && change != "delete" && change != "role-revoked" {
				require.ErrorIs(t, err, exerciseModel.ErrImportConfirmationStale.Err())
			}
			require.JSONEq(t, string(before), string(state()), "failed import must retain the independently acknowledged change")
		})
	}
}

func TestQueuedReimportRestoresPlanAndRechecksRightsBeforeMutation(t *testing.T) {
	for _, mode := range []string{"update", "blocked", "changed-target"} {
		t.Run(mode, func(t *testing.T) {
			db := testhelpers.SetupTestDB(t)
			ctx := context.Background()
			repo := exerciseRepo.New(db.Queries)
			user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "queued-plan@test.test", importJobT0)
			user.Role = rbac.RoleSuperAdmin
			_, err := userRepo.New(db.Queries).Create(ctx, user)
			require.NoError(t, err)
			e, err := exerciseModel.NewExercise("Queued target", "", nil, user.ID, importJobT0)
			require.NoError(t, err)
			e, err = repo.Create(ctx, e)
			require.NoError(t, err)
			draft, err := repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "prior working copy"}, importJobT0, uuid.NullUUID{})
			require.NoError(t, err)
			sources := newPostgresSources(t, db)
			sources.stage(archiveUpID, user.ID, mustExerciseArchive(t, `{"name":"Queued updated name"}`, `[{"status":"draft","admin_note":"queued import content","variants":[]}]`), mediaModel.UploadPurposeExerciseArchive)
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: newFakeMedia(), CatalogUoW: postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))})
			var args jobsModel.ExerciseImportArgs
			uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources, Images: &fakeImages{}, Queue: func(_ context.Context, in jobsModel.ExerciseImportArgs) error { args = in; return nil }})
			in := exercise.PreviewArchiveInput{UploadID: archiveUpID, Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: e.ID}}}
			preview, err := uc.PreviewArchiveImport(ctx, exercise.Actor{UserID: user.ID, Role: user.Role}, in)
			require.NoError(t, err)
			id, err := uc.QueueArchiveImport(ctx, exercise.QueueImportInput{Source: exerciseModel.ImportSourceUpload, SourceID: archiveUpID, CreatedBy: user.ID, Options: exercise.ImportOptions{Targets: in.Targets, ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: ".", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: true}}}})
			require.NoError(t, err)
			row, err := db.Queries.GetExerciseImportJob(ctx, id)
			require.NoError(t, err)
			require.Contains(t, string(row.Options), e.ID.String())
			require.Contains(t, string(row.Options), preview.Items[0].ConfirmationRevision)
			require.NotEmpty(t, row.PlanHash)
			stale := args
			stale.Generation++
			require.NoError(t, uc.RunArchiveImport(ctx, stale))
			current, err := repo.GetDraft(ctx, e.ID)
			require.NoError(t, err)
			require.Equal(t, draft.AdminNote, current.AdminNote)
			if mode == "blocked" {
				_, err = db.Pool.Exec(ctx, `UPDATE users SET status='blocked' WHERE id=$1`, user.ID)
				require.NoError(t, err)
			}
			if mode == "changed-target" {
				_, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "saved after queueing"}, importJobT0, uuid.NullUUID{})
				require.NoError(t, err)
			}
			require.NoError(t, uc.RunArchiveImport(ctx, args))
			job, err := uc.ArchiveImportJob(ctx, id, user.ID)
			require.NoError(t, err)
			current, err = repo.GetDraft(ctx, e.ID)
			require.NoError(t, err)
			require.Equal(t, draft.ID, current.ID)
			if mode == "update" {
				require.Equal(t, exerciseModel.ImportJobDone, job.State)
				require.Equal(t, 1, job.Updated)
				require.Zero(t, job.Created)
				require.Equal(t, "Queued updated name", current.Name)
			} else {
				require.Equal(t, exerciseModel.ImportJobFailed, job.State)
				require.Empty(t, job.Results)
				require.NotEqual(t, "Queued updated name", current.Name)
			}
		})
	}
}

func TestConcurrentReimportsWithReversedChoicesSerializeWithoutDeadlock(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo := exerciseRepo.New(db.Queries)
	user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "lock-order@test.test", importJobT0)
	user.Role = rbac.RoleSuperAdmin
	_, err := userRepo.New(db.Queries).Create(ctx, user)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 2)
	for i, name := range []string{"Lock order first", "Lock order second"} {
		e, err := exerciseModel.NewExercise(name, "", nil, user.ID, importJobT0)
		require.NoError(t, err)
		e, err = repo.Create(ctx, e)
		require.NoError(t, err)
		ids[i] = e.ID
		_, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "original"}, importJobT0, uuid.NullUUID{})
		require.NoError(t, err)
	}
	archive, err := exercise.BuildArchiveV1(map[string][]byte{"a/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Imported first"},"versions":[{"status":"draft","variants":[]}]}`), "b/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Imported second"},"versions":[{"status":"draft","variants":[]}]}`)})
	require.NoError(t, err)
	sources := newPostgresSources(t, db)
	firstSource, secondSource := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	sources.stage(firstSource, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
	sources.stage(secondSource, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: newFakeMedia(), CatalogUoW: postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))})
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: user.ID, Role: user.Role}
	requests := make([]exercise.CommitArchiveInput, 2)
	for i, id := range []uuid.UUID{firstSource, secondSource} {
		in := exercise.PreviewArchiveInput{UploadID: id, Targets: []exercise.ImportTarget{{ItemKey: "a", Mode: "update", ExerciseID: ids[0]}, {ItemKey: "b", Mode: "update", ExerciseID: ids[1]}}}
		if i == 1 {
			in.Targets[0], in.Targets[1] = in.Targets[1], in.Targets[0]
		}
		preview, err := uc.PreviewArchiveImport(ctx, actor, in)
		require.NoError(t, err)
		requests[i].PreviewArchiveInput = in
		for _, item := range preview.Items {
			requests[i].ConfirmedUpdates = append(requests[i].ConfirmedUpdates, exercise.ConfirmedImportUpdate{ItemKey: item.ItemKey, Revision: item.ConfirmationRevision, ReplaceDraft: true})
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, request := range requests {
		go func() { <-start; _, err := uc.CommitArchiveImport(ctx, actor, request); results <- err }()
	}
	close(start)
	one, two := <-results, <-results
	if one == nil {
		require.ErrorIs(t, two, exerciseModel.ErrImportConfirmationStale.Err())
	} else {
		require.ErrorIs(t, one, exerciseModel.ErrImportConfirmationStale.Err())
		require.NoError(t, two)
	}
	require.NoError(t, ctx.Err(), "reversed input order must not deadlock the family locks")
}
