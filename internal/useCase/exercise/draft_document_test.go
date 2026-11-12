package exercise_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The working copy as the document: YAML of the archive's document, keys in order, a secret value never in it, a reader
// who may not see flags gets none, a missing exercise is the usual error.
func TestDraftDocument(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := newUC(q, newFakeMedia())
	exID, publishedID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants := draftVariants("hunter2-secret", true)
	variants[0].Tasks[0].Check = exerciseModel.FlagCheck("ICE{the-answer}")
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, Name: "Task", PublishedVersionID: uuid.NullUUID{UUID: publishedID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), publishedID).Return(postgres.ExerciseVersion{
		ID: publishedID, ExerciseID: exID, Status: "published", Variants: mustMarshal(t, variants),
	}, nil).AnyTimes()

	withFlags, err := uc.DraftDocument(ctx, exID, true)
	require.NoError(t, err)
	text := string(withFlags)
	require.True(t, strings.HasPrefix(text, "schema: cib-exercise/1.0\n"), text)
	require.NotContains(t, text, "hunter2-secret", "a secret value never leaves")
	require.Contains(t, text, "secret: true")
	require.Contains(t, text, "ICE{the-answer}")
	require.Less(t, strings.Index(text, "exercise:"), strings.Index(text, "versions:"), "keys keep the order of the document")

	noFlags, err := uc.DraftDocument(ctx, exID, false)
	require.NoError(t, err)
	require.NotContains(t, string(noFlags), "ICE{the-answer}")
	require.NotContains(t, string(noFlags), "hunter2-secret")
}
