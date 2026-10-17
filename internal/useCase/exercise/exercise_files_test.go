package exercise_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

var (
	filesTaskID   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000f0001")
	filesAuthorID = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000f0002")
	filesOtherID  = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000f0003")
	filesFirstID  = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000f0011")
	filesSecondID = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000f0012")
	filesHeldID   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000f0013")
)

const filesHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// expectFilesAdoptedAsIs lets a test that does not care about per-task storage pass the adoption through:
// every file has its own hash and the task keeps the id it was given.
func expectFilesAdoptedAsIs(q *postgresMocks.MockQuerier) {
	q.EXPECT().ListFileHashes(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []uuid.UUID) ([]postgres.ListFileHashesRow, error) {
		rows := make([]postgres.ListFileHashesRow, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, postgres.ListFileHashesRow{ID: id, ContentHash: "hash-" + id.String()})
		}
		return rows, nil
	}).AnyTimes()
	q.EXPECT().ClaimExerciseFile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.ClaimExerciseFileParams) (uuid.UUID, error) {
		return arg.FileID, nil
	}).AnyTimes()
}

func writtenAttachments(t *testing.T, raw []byte) []exerciseModel.AttachmentRef {
	t.Helper()
	var variants []exerciseModel.Variant
	require.NoError(t, json.Unmarshal(raw, &variants))
	var refs []exerciseModel.AttachmentRef
	for _, variant := range variants {
		for _, task := range variant.Tasks {
			refs = append(refs, task.Attachments...)
		}
	}
	return refs
}

func TestLookupExerciseFile_FoundAndMissing(t *testing.T) {
	ctx := context.Background()
	t.Run("found", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		q.EXPECT().GetExerciseFileByHash(gomock.Any(), postgres.GetExerciseFileByHashParams{ExerciseID: filesTaskID, ContentHash: filesHash}).
			Return(postgres.GetExerciseFileByHashRow{ID: filesHeldID, Name: "notes.txt", SizeBytes: 42}, nil)
		got, err := newUC(q, newFakeMedia()).LookupExerciseFile(ctx, filesTaskID, filesHash)
		require.NoError(t, err)
		require.Equal(t, exercise.FileLookup{Found: true, FileID: filesHeldID, Name: "notes.txt", Size: 42}, got)
	})
	t.Run("missing", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		q.EXPECT().GetExerciseFileByHash(gomock.Any(), postgres.GetExerciseFileByHashParams{ExerciseID: filesTaskID, ContentHash: filesHash}).
			Return(postgres.GetExerciseFileByHashRow{}, errNoRows())
		got, err := newUC(q, newFakeMedia()).LookupExerciseFile(ctx, filesTaskID, filesHash)
		require.NoError(t, err)
		require.Equal(t, exercise.FileLookup{}, got)
	})
}

// Two uploads of the same bytes into one task end as one stored file: the draft names the task's file twice.
func TestSaveDraft_DedupesSameBytes(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	expectUserNames(q)
	q.EXPECT().GetDraftVersion(gomock.Any(), filesTaskID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), filesTaskID).Return(postgres.Exercise{ID: filesTaskID}, nil).AnyTimes()
	q.EXPECT().ListFileOwners(gomock.Any(), gomock.Any()).Return([]postgres.ListFileOwnersRow{
		{ID: filesFirstID, CreatedBy: uuid.NullUUID{UUID: filesAuthorID, Valid: true}},
		{ID: filesSecondID, CreatedBy: uuid.NullUUID{UUID: filesAuthorID, Valid: true}},
	}, nil)
	q.EXPECT().ListFileHashes(gomock.Any(), gomock.Any()).Return([]postgres.ListFileHashesRow{
		{ID: filesFirstID, ContentHash: filesHash}, {ID: filesSecondID, ContentHash: filesHash},
	}, nil)
	// The task already holds these bytes as an older file: that one is the canonical id.
	q.EXPECT().ClaimExerciseFile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.ClaimExerciseFileParams) (uuid.UUID, error) {
		require.Equal(t, filesTaskID, arg.ExerciseID)
		require.Equal(t, filesHash, arg.ContentHash)
		require.Equal(t, filesFirstID, arg.FileID)
		require.False(t, arg.CreatedAt.IsZero())
		return filesHeldID, nil
	}).Times(1)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)
	media := newFakeMedia()

	_, err := newUC(q, media).SaveDraft(context.Background(), filesTaskID, exercise.SaveDraftInput{
		SavedBy: filesAuthorID, Variants: variantsWithFiles(filesFirstID, filesSecondID),
	})
	require.NoError(t, err)
	require.Equal(t, []exerciseModel.AttachmentRef{{FileID: filesHeldID, Name: "a.zip"}, {FileID: filesHeldID, Name: "b.zip"}},
		writtenAttachments(t, captured.Variants))
	require.Equal(t, []uuid.UUID{filesHeldID, filesHeldID}, media.replaced[captured.NewID])
}

// A file uploaded by someone else but held by the task (a co-author's upload found by the hash lookup)
// may be attached.
func TestSaveDraft_AcceptsFileHeldByTask(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	expectUserNames(q)
	q.EXPECT().GetDraftVersion(gomock.Any(), filesTaskID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), filesTaskID).Return(postgres.Exercise{ID: filesTaskID}, nil).AnyTimes()
	q.EXPECT().ListFileOwners(gomock.Any(), gomock.Any()).Return([]postgres.ListFileOwnersRow{
		{ID: filesHeldID, CreatedBy: uuid.NullUUID{UUID: filesOtherID, Valid: true}},
	}, nil)
	q.EXPECT().ExerciseHoldsFile(gomock.Any(), postgres.ExerciseHoldsFileParams{ExerciseID: filesTaskID, FileID: filesHeldID}).Return(true, nil)
	q.EXPECT().ListFileExerciseIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	expectFilesAdoptedAsIs(q)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	_, err := newUC(q, newFakeMedia()).SaveDraft(context.Background(), filesTaskID, exercise.SaveDraftInput{
		SavedBy: filesAuthorID, Variants: variantsWithFiles(filesHeldID),
	})
	require.NoError(t, err)
	require.Equal(t, []exerciseModel.AttachmentRef{{FileID: filesHeldID, Name: "a.zip"}}, writtenAttachments(t, captured.Variants))
}

// Two archive paths with equal bytes are uploaded once and stored as one file of the task.
func TestImportExerciseArchive_UploadsEachContentOnce(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	var written []exerciseModel.Variant
	expectImport(q, func(variants []exerciseModel.Variant) { written = variants })
	var claimed []postgres.ClaimExerciseFileParams
	q.EXPECT().ListFileHashes(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []uuid.UUID) ([]postgres.ListFileHashesRow, error) {
		rows := make([]postgres.ListFileHashesRow, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, postgres.ListFileHashesRow{ID: id, ContentHash: filesHash})
		}
		return rows, nil
	})
	q.EXPECT().ClaimExerciseFile(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.ClaimExerciseFileParams) (uuid.UUID, error) {
		claimed = append(claimed, arg)
		return arg.FileID, nil
	})
	media := newFakeMedia()
	archive := linkedArchive(t, "draft", `[{"tasks":[{"name":"Перше","attachments":[{"file":"files/a.txt"},{"file":"other/b.txt"}]}]}]`,
		map[string]string{"files/a.txt": "same bytes", "other/b.txt": "same bytes"})

	created, err := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: media}).
		ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
	require.Len(t, media.uploads, 1)
	require.Len(t, claimed, 1)
	require.Equal(t, created.ID, claimed[0].ExerciseID)
	require.Len(t, written, 1)
	stored := media.uploads[0].ID
	require.Equal(t, []exerciseModel.AttachmentRef{{FileID: stored, Name: "a.txt"}, {FileID: stored, Name: "b.txt"}},
		written[0].Tasks[0].Attachments)
}
