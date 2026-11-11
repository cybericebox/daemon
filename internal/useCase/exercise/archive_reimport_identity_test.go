package exercise_test

import (
	"context"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
)

// expectNoUsage answers the usage questions of an import report: no event attaches the exercise, no test lab runs.
func expectNoUsage(q *postgresMocks.MockQuerier) {
	q.EXPECT().
		ListExerciseActiveAttachments(gomock.Any(), gomock.Any()).
		Return([]postgres.ListExerciseActiveAttachmentsRow{}, nil).
		AnyTimes()
	q.EXPECT().
		ListExerciseChallengeAnswerCounts(gomock.Any(), gomock.Any()).
		Return([]postgres.ListExerciseChallengeAnswerCountsRow{}, nil).
		AnyTimes()
	q.EXPECT().
		CountLeasedExerciseTestDeploys(gomock.Any(), gomock.Any()).
		Return(int32(0), nil).
		AnyTimes()
}

// The bootstrap size check runs while the document is read, so an import into an existing exercise refuses an
// oversized bootstrap file exactly as a new import does, before the target is touched.
func TestReimportRefusesOversizedBootstrapFile(t *testing.T) {
	target := uuid.Must(uuid.NewV7())
	document := `{"schema":"cib-exercise/1.0","exercise":{"id":"` + target.String() + `","name":"Sized"},"versions":[{"variants":[{"topology":{"devices":[{"name":"web","type":"node","bootstrap":{"files":[{"path":"/etc/app.bin","source":"files/big.bin"}]}}]}}]}]}`
	archive, err := exercise.BuildArchiveV1(map[string][]byte{
		"exercise.json": []byte(document),
		"files/big.bin": make([]byte, exerciseModel.MaxBootstrapFileBytes+1),
	})
	require.NoError(t, err)
	sources := newFakeSources()
	sources.stage(archiveUpID, importer, archive, mediaModel.UploadPurposeExerciseArchive)
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q, newFakeMedia())
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: importer, Role: rbac.RoleSuperAdmin}
	_, err = uc.PreviewArchiveImport(context.Background(), actor, exercise.PreviewArchiveInput{UploadID: archiveUpID,
		Targets: []exercise.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: target}}})
	require.ErrorIs(t, err, exerciseModel.ErrBootstrapTooLarge.Err())
}
