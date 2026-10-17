package exercise_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// importArchive runs ImportExerciseArchive with a repository that expects no writes.
func importArchive(t *testing.T, archive []byte, password string) error {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive, Password: password})
	return err
}

func archiveReason(t *testing.T, err error) string {
	t.Helper()
	require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
	var withContext interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &withContext)
	reason, _ := withContext.PublicContext()["reason"].(string)
	return reason
}

func TestImportExerciseArchive_StatusRequired(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"no status"}`,
		`[{"status":"draft","variants":[{"tasks":[{"name":"t"}]}]},{"variants":[{"tasks":[{"name":"t"}]}]}]`)
	require.Equal(t, "versions[2].status: required", archiveReason(t, importArchive(t, archive, "")))
}

func TestImportExerciseArchive_NoVersionsInvalid(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"no versions"}`, `[]`)
	require.Contains(t, archiveReason(t, importArchive(t, archive, "")), "versions")
}

func TestImportExerciseArchive_TwoDraftsInvalid(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"two drafts"}`,
		`[{"status":"draft","variants":[{"tasks":[{"name":"t"}]}]},{"status":"draft","variants":[{"tasks":[{"name":"t"}]}]}]`)
	require.Contains(t, archiveReason(t, importArchive(t, archive, "")), "versions[2].status")
}

func TestImportExerciseArchive_TwoPublishedInvalid(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"two published"}`, `[{"status":"published"},{"status":"published"}]`)
	require.Contains(t, archiveReason(t, importArchive(t, archive, "")), "versions[2].status")
}

const secretVersions = `[{"status":"draft","variants":[{"topology":{"devices":[{"name":"d","type":"container","env_vars":[{"name":"K","value":"plain","secret":true}]}]}}]}]`

func TestImportExerciseArchive_PlaintextSecretNeedsProtectedArchive(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"secret"}`, secretVersions)
	reason := archiveReason(t, importArchive(t, archive, ""))
	require.Contains(t, reason, "versions[1].variants[1].topology.devices[1].env_vars[1]")
}

// A password typed for an archive that was not protected must not let a plaintext secret in.
func TestImportExerciseArchive_PasswordForUnprotectedArchiveKeepsSecretGuard(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"secret"}`, secretVersions)
	archiveReason(t, importArchive(t, archive, "typed anyway"))
}

func TestExpandExerciseArchiveBundle_PasswordOnlyForProtectedSource(t *testing.T) {
	files := map[string][]byte{
		"a/exercise.json": []byte(`{"schema":"cib-exercise/1"}`),
		"b/exercise.json": []byte(`{"schema":"cib-exercise/1"}`),
	}
	plain, err := exercise.BuildArchiveV1(files)
	require.NoError(t, err)
	imports, err := exercise.ExpandExerciseArchiveBundle(plain, "typed anyway", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Len(t, imports, 2)
	for _, item := range imports {
		require.Empty(t, item.Password, "an unprotected source gives unprotected payloads")
		_, readErr := exercise.ReadArchiveV1(bytes.NewReader(item.Archive), exercise.DefaultLimits())
		require.NoError(t, readErr)
	}

	protected, err := exercise.BuildProtectedArchiveV1(files, "pw")
	require.NoError(t, err)
	imports, err = exercise.ExpandExerciseArchiveBundle(protected, "pw", exercise.DefaultLimits())
	require.NoError(t, err)
	require.Len(t, imports, 2)
	for _, item := range imports {
		require.Equal(t, "pw", item.Password)
		_, readErr := exercise.ReadProtectedArchiveV1(item.Archive, "pw", exercise.DefaultLimits())
		require.NoError(t, readErr)
	}
}
