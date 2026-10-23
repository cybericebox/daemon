package exercise_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// pgconnUniqueViolation simulates a unique-violation on exercises.name, as
// Postgres would report it for a duplicate CreateExercise/rename.
var pgconnUniqueViolation = pgconn.PgError{
	Code:   pgerrcode.UniqueViolation,
	Detail: `Key (name)=(dup name) already exists.`,
}

// fakeMedia is a hand-rolled stub for exercise.IMedia — no gomock needed
// since the use case only calls it fire-and-forget on delete/replace.
type fakeMedia struct {
	replaced map[uuid.UUID][]uuid.UUID
	removed  []uuid.UUID
	uploads  []fakeUpload
}

// fakeUpload is one UploadFile call: what the archive import sent to media.
type fakeUpload struct {
	ID          uuid.UUID
	Name        string
	ContentType string
	Body        []byte
}

func (f *fakeMedia) UploadFile(_ context.Context, name, contentType string, body io.Reader, _ uuid.UUID) (mediaModel.File, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return mediaModel.File{}, err
	}
	id := uuid.FromStringOrNil(fmt.Sprintf("0192f0a0-0000-7000-8000-%012d", len(f.uploads)+1))
	f.uploads = append(f.uploads, fakeUpload{ID: id, Name: name, ContentType: contentType, Body: data})
	return mediaModel.File{ID: id, Name: name, ContentType: contentType}, nil
}

func newFakeMedia() *fakeMedia { return &fakeMedia{replaced: map[uuid.UUID][]uuid.UUID{}} }

func (f *fakeMedia) ReplaceReferences(_ context.Context, _ string, refID uuid.UUID, fileIDs []uuid.UUID) error {
	f.replaced[refID] = fileIDs
	return nil
}
func (f *fakeMedia) RemoveReferences(_ context.Context, _ string, refID uuid.UUID) error {
	f.removed = append(f.removed, refID)
	return nil
}
func (f *fakeMedia) RemoveReferencesBatch(_ context.Context, _ string, refIDs []uuid.UUID) error {
	f.removed = append(f.removed, refIDs...)
	return nil
}

func newUC(q exercise.IRepository, m exercise.IMedia) *exercise.ExerciseUseCase {
	return newTestExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: nil, Media: m})
}

// expectEditableExercise stubs the loadEditable pre-read (exists, active,
// nothing published) every catalog-content mutation performs first.
func expectEditableExercise(q *postgresMocks.MockQuerier, exID uuid.UUID) {
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil).AnyTimes()
}

// expectUserNames lets a test that does not care about author names pass the name lookup through.
func expectUserNames(q *postgresMocks.MockQuerier) {
	q.EXPECT().ListUserNames(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
}

func TestCreateExercise_WritesDomainRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectUserNames(q)
	uc := newUC(q, newFakeMedia())
	adminID := uuid.Must(uuid.NewV7())

	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			if arg.Name != "SQLi basics" || len(arg.Tags) != 1 || arg.Tags[0] != "web" {
				t.Fatalf("unexpected params: %+v", arg)
			}
			if arg.ID == uuid.Nil || arg.CreatedAt.IsZero() {
				t.Fatal("domain defaults must be set")
			}
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, Description: arg.Description,
				Tags: arg.Tags, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy,
				UpdatedAt: arg.UpdatedAt, UpdatedBy: arg.UpdatedBy}, nil
		})

	v, err := uc.CreateExercise(context.Background(), exercise.CreateExerciseInput{
		Name: "SQLi basics", Description: "d", Tags: []string{"Web"}, CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("CreateExercise: %v", err)
	}
	if v.Name != "SQLi basics" || v.Tags[0] != "web" {
		t.Fatalf("view mismatch: %+v", v)
	}
}

func TestCreateExercise_DuplicateName(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())

	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).
		Return(postgres.Exercise{}, &pgconnUniqueViolation)

	_, err := uc.CreateExercise(context.Background(), exercise.CreateExerciseInput{Name: "dup name"})
	if !errors.Is(err, exerciseModel.ErrExerciseExists.Err()) {
		t.Fatalf("want ErrExerciseExists, got %v", err)
	}
}

func TestUpdateExerciseIdentity_ConflictDiscrimination(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	id := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	row := postgres.Exercise{ID: id, Name: "old name", CreatedAt: now, UpdatedAt: now}

	// Row exists, write hits 0 rows, re-read finds it → 409.
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(row, nil)
	q.EXPECT().SaveExerciseDraftIdentity(gomock.Any(), gomock.Any()).Return(postgres.SaveExerciseDraftIdentityRow{}, pgx.ErrNoRows)
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(row, nil)

	_, err := uc.UpdateExerciseIdentity(context.Background(), exercise.UpdateExerciseInput{
		ID: id, Name: "new name", Description: "", Tags: nil,
	})
	if !errors.Is(err, exerciseModel.ErrExerciseModified.Err()) {
		t.Fatalf("want ErrExerciseModified, got %v", err)
	}

	// Row vanished between read and write → 404.
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(row, nil)
	q.EXPECT().SaveExerciseDraftIdentity(gomock.Any(), gomock.Any()).Return(postgres.SaveExerciseDraftIdentityRow{}, pgx.ErrNoRows)
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{}, pgx.ErrNoRows)

	_, err = uc.UpdateExerciseIdentity(context.Background(), exercise.UpdateExerciseInput{
		ID: id, Name: "new name", Description: "", Tags: nil,
	})
	if !errors.Is(err, exerciseModel.ErrExerciseNotFound.Err()) {
		t.Fatalf("want ErrExerciseNotFound, got %v", err)
	}
}

func TestDeleteExercise_CleansReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeMedia()
	uc := newUC(q, m)
	id := uuid.Must(uuid.NewV7())
	v1, v2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	q.EXPECT().ListExerciseUsageEvents(gomock.Any(), gomock.Any()).Return([]postgres.ListExerciseUsageEventsRow{}, nil)
	q.EXPECT().ListExerciseVersionIDs(gomock.Any(), id).Return([]uuid.UUID{v1, v2}, nil)
	q.EXPECT().DeleteExercise(gomock.Any(), id).Return(int64(1), nil)

	if err := uc.DeleteExercise(context.Background(), id); err != nil {
		t.Fatalf("DeleteExercise: %v", err)
	}
	if len(m.removed) != 2 {
		t.Fatalf("both versions' references must be removed: %v", m.removed)
	}
}

func TestDeleteExercise_RejectsUsedExercise(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	id := uuid.Must(uuid.NewV7())
	q.EXPECT().ListExerciseUsageEvents(gomock.Any(), gomock.Any()).Return([]postgres.ListExerciseUsageEventsRow{
		{ID: uuid.Must(uuid.NewV7()), Name: "Spring CTF", Archived: true},
	}, nil)

	if err := uc.DeleteExercise(context.Background(), id); !errors.Is(err, exerciseModel.ErrExerciseInUse.Err()) {
		t.Fatalf("want ErrExerciseInUse, got %v", err)
	}
}

func TestDeleteExercise_ForeignKeyRaceReportsInUse(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	id := uuid.Must(uuid.NewV7())
	q.EXPECT().ListExerciseUsageEvents(gomock.Any(), gomock.Any()).Return([]postgres.ListExerciseUsageEventsRow{}, nil)
	q.EXPECT().ListExerciseVersionIDs(gomock.Any(), id).Return(nil, nil)
	q.EXPECT().DeleteExercise(gomock.Any(), id).Return(int64(0), &pgconn.PgError{Code: pgerrcode.ForeignKeyViolation})
	q.EXPECT().ListExerciseUsageEvents(gomock.Any(), gomock.Any()).Return([]postgres.ListExerciseUsageEventsRow{
		{ID: uuid.Must(uuid.NewV7()), Name: "Attached meanwhile"},
	}, nil)

	if err := uc.DeleteExercise(context.Background(), id); !errors.Is(err, exerciseModel.ErrExerciseInUse.Err()) {
		t.Fatalf("want ErrExerciseInUse, got %v", err)
	}
}

func TestGetExerciseUsage_MapsEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	id, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), id).Return(postgres.Exercise{ID: id}, nil)
	q.EXPECT().ListExerciseUsageEvents(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListExerciseUsageEventsParams) ([]postgres.ListExerciseUsageEventsRow, error) {
			if arg.ExerciseID != id || arg.Now.IsZero() {
				t.Fatalf("unexpected usage params: %+v", arg)
			}
			return []postgres.ListExerciseUsageEventsRow{{ID: eventID, Name: "Spring CTF", Archived: true}}, nil
		})
	usage, err := uc.GetExerciseUsage(context.Background(), id)
	if err != nil || len(usage.Events) != 1 || usage.Events[0].ID != eventID || usage.Events[0].Name != "Spring CTF" || !usage.Events[0].Archived {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
}

func TestListExercises_ArchivedFilter(t *testing.T) {
	archivedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ in, want string }{{"", "exclude"}, {"exclude", "exclude"}, {"only", "only"}, {"bogus", "exclude"}} {
		t.Run("cursor/"+tc.in, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			uc := newUC(q, newFakeMedia())
			q.EXPECT().ListExercisesCursor(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.ListExercisesCursorParams) ([]postgres.ListExercisesCursorRow, error) {
					if arg.Archived != tc.want {
						t.Fatalf("list archived=%q, want %q", arg.Archived, tc.want)
					}
					return []postgres.ListExercisesCursorRow{{Exercise: postgres.Exercise{ID: uuid.Must(uuid.NewV7()), Name: "x",
						ArchivedAt: pgtype.Timestamptz{Time: archivedAt, Valid: true}}, Status: "archived"}}, nil
				})
			q.EXPECT().CountExercises(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.CountExercisesParams) (int64, error) {
					if arg.Archived != tc.want {
						t.Fatalf("count archived=%q, want %q", arg.Archived, tc.want)
					}
					return 1, nil
				})
			res, err := uc.ListExercises(context.Background(), exercise.ExercisesFilter{Archived: tc.in, PageSize: 10})
			if err != nil || len(res.Exercises) != 1 || res.Exercises[0].ArchivedAt == nil || !res.Exercises[0].ArchivedAt.Equal(archivedAt) {
				t.Fatalf("list result: %+v err=%v", res, err)
			}
		})
	}
	t.Run("page/only", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := postgresMocks.NewMockQuerier(ctrl)
		uc := newUC(q, newFakeMedia())
		q.EXPECT().ListExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, arg postgres.ListExercisesPageParams) ([]postgres.Exercise, error) {
				if arg.Archived != "only" {
					t.Fatalf("page archived=%q", arg.Archived)
				}
				return nil, nil
			})
		q.EXPECT().CountExercisesPage(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, arg postgres.CountExercisesPageParams) (int64, error) {
				if arg.Archived != "only" {
					t.Fatalf("page count archived=%q", arg.Archived)
				}
				return 0, nil
			})
		if _, err := uc.ListExercises(context.Background(), exercise.ExercisesFilter{Archived: "only", Page: 1, PageSize: 10}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestGetExercise_HasChangesAndArchivedAt(t *testing.T) {
	valid := func() uuid.NullUUID { return uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true} }
	yes, no := true, false
	archivedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		draft, published uuid.NullUUID
		differs          *bool
		want             bool
	}{
		{"never published", valid(), uuid.NullUUID{}, nil, true},
		{"published, no draft row", uuid.NullUUID{}, valid(), nil, false},
		{"draft equals published", valid(), valid(), &no, false},
		{"draft differs", valid(), valid(), &yes, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			uc := newUC(q, newFakeMedia())
			exID := uuid.Must(uuid.NewV7())
			q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{
				ID: exID, DraftVersionID: tc.draft, PublishedVersionID: tc.published,
				ArchivedAt: pgtype.Timestamptz{Time: archivedAt, Valid: true},
			}, nil)
			if tc.differs != nil {
				q.EXPECT().ExerciseDraftDiffersFromPublished(gomock.Any(), exID).Return(*tc.differs, nil)
			}
			view, err := uc.GetExercise(context.Background(), exID)
			if err != nil || view.HasChanges != tc.want || view.ArchivedAt == nil || !view.ArchivedAt.Equal(archivedAt) {
				t.Fatalf("view=%+v err=%v, want HasChanges=%t", view, err, tc.want)
			}
		})
	}
}

// Explicit unit-test transaction port. Production always opens a real UoW;
// these narrow tests still inspect their existing aggregate/media doubles.
type testCatalogUnit struct{}

func (testCatalogUnit) Save() error    { return nil }
func (testCatalogUnit) Restore() error { return nil }

type testCatalogWorker struct {
	q     exercise.IRepository
	media exercise.IMedia
}
type testCatalogQueries struct {
	postgres.Querier
	media exercise.IMedia
}

func (w testCatalogWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	return ctx, testCatalogQueries{Querier: w.q.(postgres.Querier), media: w.media}, testCatalogUnit{}, nil
}
func (q testCatalogQueries) LockExerciseForImport(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	return id, nil
}
func (q testCatalogQueries) ReplaceFileReferences(ctx context.Context, in postgres.ReplaceFileReferencesParams) error {
	return q.media.ReplaceReferences(ctx, in.RefType, in.RefID, in.FileIds)
}
func (q testCatalogQueries) DeleteFileReferences(ctx context.Context, in postgres.DeleteFileReferencesParams) (int64, error) {
	return 0, q.media.RemoveReferences(ctx, in.RefType, in.RefID)
}
func (q testCatalogQueries) DeleteFileReferencesBatch(ctx context.Context, in postgres.DeleteFileReferencesBatchParams) (int64, error) {
	return 0, q.media.RemoveReferencesBatch(ctx, in.RefType, in.RefIds)
}
func newTestExerciseUseCase(deps exercise.Dependencies) *exercise.ExerciseUseCase {
	deps.CatalogUoW = testCatalogWorker{deps.Repo, deps.Media}
	return exercise.NewExerciseUseCase(deps)
}

func (q testCatalogQueries) TryCreateImportedExercise(ctx context.Context, p postgres.TryCreateImportedExerciseParams) (postgres.Exercise, error) {
	row, err := q.Querier.CreateExercise(ctx, postgres.CreateExerciseParams(p))
	var duplicate *pgconn.PgError
	if errors.As(err, &duplicate) && duplicate.Code == "23505" {
		return postgres.Exercise{}, pgx.ErrNoRows
	}
	return row, err
}
