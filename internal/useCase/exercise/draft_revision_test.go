package exercise_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// The working copy is written only on top of the revision the editor started from: the use case hands it to the write as an
// exact microsecond timestamp (no revision: a first draft only), refuses a revision the platform did not issue, and turns the
// zero rows of a stale write into one 409 that names the current revision and who saved it.

var revisionAt = time.Date(2026, 11, 16, 10, 0, 0, 123456000, time.UTC)

const revisionText = "2026-11-16T10:00:00.123456Z"

func TestRevisionRoundTripsAtMicrosecondPrecision(t *testing.T) {
	version := exerciseModel.ExerciseVersion{UpdatedAt: revisionAt}
	require.Equal(t, revisionText, version.Revision())
	parsed, err := exerciseModel.ParseRevision(revisionText)
	require.NoError(t, err)
	require.True(t, revisionAt.Equal(parsed))
	require.Equal(t, revisionText, exerciseModel.FormatRevision(parsed))
	none, err := exerciseModel.ParseRevision("")
	require.NoError(t, err)
	require.True(t, none.IsZero())
	require.Equal(t, "", exerciseModel.FormatRevision(none))
}

func TestParseRevisionRefusesWhatThePlatformDidNotIssue(t *testing.T) {
	for _, text := range []string{
		"garbage",
		"2026-11-16T10:00:00Z",             // no microseconds: a client that lost precision
		"2026-11-16T10:00:00.123Z",         // milliseconds: what a JavaScript Date would send back
		"2026-11-16T10:00:00.123456+02:00", // not UTC
		"2026-11-16T10:00:00.1234567Z",     // finer than the database keeps
	} {
		_, err := exerciseModel.ParseRevision(text)
		require.ErrorIs(t, err, exerciseModel.ErrDraftRevisionInvalid.Err(), text)
	}
}

func TestSaveDraft_WritesOnTopOfTheRevisionTheEditorSent(t *testing.T) {
	s := newLinkedSave(t, nil)
	s.save(t, exercise.SaveDraftInput{Revision: revisionText, Variants: []exerciseModel.Variant{saveVariant(saveBaseID, "t", "", "", "")}})
	require.True(t, s.written.ExpectedUpdatedAt.Valid)
	require.True(t, revisionAt.Equal(s.written.ExpectedUpdatedAt.Time))
	require.False(t, s.written.IgnoreRevision, "a user's save never skips the check")
}

func TestSaveDraft_WithoutARevisionCreatesTheFirstDraftOnly(t *testing.T) {
	s := newLinkedSave(t, nil)
	s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{saveVariant(saveBaseID, "t", "", "", "")}})
	require.False(t, s.written.ExpectedUpdatedAt.Valid, "no revision is the NULL that only a first draft passes")
	require.False(t, s.written.IgnoreRevision)
}

func TestSaveDraft_RefusesAnInvalidRevisionBeforeWritingAnything(t *testing.T) {
	s := newLinkedSave(t, nil)
	_, err := s.uc.SaveDraft(context.Background(), saveExerciseID, exercise.SaveDraftInput{
		Revision: "2026-11-16T10:00:00.123Z", Variants: []exerciseModel.Variant{saveVariant(saveBaseID, "t", "", "", "")}})
	require.ErrorIs(t, err, exerciseModel.ErrDraftRevisionInvalid.Err())
	require.False(t, s.called)
}

func TestSaveDraft_AStaleRevisionIsA409WithTheCurrentRevisionAndWhoSavedIt(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	saver := uuid.Must(uuid.NewV7())
	current := revisionAt.Add(5 * time.Minute)
	q.EXPECT().GetExerciseByID(gomock.Any(), saveExerciseID).Return(postgres.Exercise{ID: saveExerciseID}, nil).AnyTimes()
	q.EXPECT().GetDraftVersion(gomock.Any(), saveExerciseID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	q.EXPECT().UpsertExerciseDraft(gomock.Any(), gomock.Any()).Return(postgres.UpsertExerciseDraftRow{}, errNoRows())
	q.EXPECT().GetDraftRevision(gomock.Any(), saveExerciseID).Return(postgres.GetDraftRevisionRow{
		ID: saveDraftID, UpdatedAt: current, UpdatedBy: uuid.NullUUID{UUID: saver, Valid: true}}, nil)
	q.EXPECT().ListUserNames(gomock.Any(), gomock.Any()).Return([]postgres.ListUserNamesRow{{ID: saver, FirstName: "Ada", LastName: "Lovelace"}}, nil)

	_, err := uc.SaveDraft(context.Background(), saveExerciseID, exercise.SaveDraftInput{
		Revision: revisionText, Variants: []exerciseModel.Variant{saveVariant(saveBaseID, "t", "", "", "")}})

	require.ErrorIs(t, err, exerciseModel.ErrDraftModified.Err())
	context := publicContext(t, err)
	require.Equal(t, exerciseModel.FormatRevision(current), context["revision"])
	require.Equal(t, "Ada Lovelace", context["savedBy"])
	require.Equal(t, "2026-11-16T10:05:00Z", context["savedAt"])
}

func TestSaveDraft_AStaleWriteToADiscardedCopyNamesNoRevision(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	q.EXPECT().GetExerciseByID(gomock.Any(), saveExerciseID).Return(postgres.Exercise{ID: saveExerciseID}, nil).AnyTimes()
	q.EXPECT().GetDraftVersion(gomock.Any(), saveExerciseID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	q.EXPECT().UpsertExerciseDraft(gomock.Any(), gomock.Any()).Return(postgres.UpsertExerciseDraftRow{}, errNoRows())
	q.EXPECT().GetDraftRevision(gomock.Any(), saveExerciseID).Return(postgres.GetDraftRevisionRow{}, errNoRows())

	_, err := uc.SaveDraft(context.Background(), saveExerciseID, exercise.SaveDraftInput{
		Revision: revisionText, Variants: []exerciseModel.Variant{saveVariant(saveBaseID, "t", "", "", "")}})

	require.ErrorIs(t, err, exerciseModel.ErrDraftModified.Err())
	require.Equal(t, "", publicContext(t, err)["revision"])
}

func TestDiscardDraft_SendsTheRevisionAndRefusesAStaleOne(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	expectUserNames(q)
	q.EXPECT().DiscardExerciseDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.DiscardExerciseDraftParams) (uuid.UUID, error) {
			require.True(t, arg.ExpectedUpdatedAt.Valid)
			require.True(t, revisionAt.Equal(arg.ExpectedUpdatedAt.Time))
			require.False(t, arg.IgnoreRevision)
			return uuid.Nil, errNoRows()
		})
	q.EXPECT().GetDraftRevision(gomock.Any(), exID).Return(postgres.GetDraftRevisionRow{UpdatedAt: revisionAt.Add(time.Second)}, nil).Times(2)

	err := uc.DiscardDraft(context.Background(), exID, revisionText)

	require.ErrorIs(t, err, exerciseModel.ErrDraftModified.Err())
	require.Equal(t, exerciseModel.FormatRevision(revisionAt.Add(time.Second)), publicContext(t, err)["revision"])
}

func TestDiscardDraft_RefusesAnInvalidRevision(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q, newFakeMedia())
	err := uc.DiscardDraft(context.Background(), uuid.Must(uuid.NewV7()), "nonsense")
	require.ErrorIs(t, err, exerciseModel.ErrDraftRevisionInvalid.Err())
}

func TestUpdateExerciseIdentity_SendsTheRevisionAndAStaleOneIsA409(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q, newFakeMedia())
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	expectUserNames(q)
	q.EXPECT().SaveExerciseDraftIdentity(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SaveExerciseDraftIdentityParams) (postgres.SaveExerciseDraftIdentityRow, error) {
			require.True(t, arg.ExpectedDraftUpdatedAt.Valid)
			require.True(t, revisionAt.Equal(arg.ExpectedDraftUpdatedAt.Time))
			require.False(t, arg.IgnoreRevision)
			return postgres.SaveExerciseDraftIdentityRow{}, errNoRows()
		})
	q.EXPECT().GetDraftRevision(gomock.Any(), exID).Return(postgres.GetDraftRevisionRow{UpdatedAt: revisionAt.Add(time.Minute)}, nil).Times(2)

	_, err := uc.UpdateExerciseIdentity(context.Background(), exercise.UpdateExerciseInput{
		ID: exID, Name: "renamed", Revision: revisionText, UpdatedBy: uuid.Must(uuid.NewV7())})

	require.ErrorIs(t, err, exerciseModel.ErrDraftModified.Err())
}
