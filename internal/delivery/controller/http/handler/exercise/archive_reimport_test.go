package exercise_test

import (
	"context"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/pagination"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

type previewCalls struct {
	input   exerciseUseCase.PreviewArchiveInput
	actor   exerciseUseCase.Actor
	view    exerciseUseCase.ArchiveImportPreview
	err     error
	search  string
	cursor  uuid.UUID
	limit   int
	targets pagination.CursorPage[exerciseUseCase.ImportTargetExercise]
	calls   int
}

func (f *fakeUC) PreviewArchiveImport(_ context.Context, actor exerciseUseCase.Actor, in exerciseUseCase.PreviewArchiveInput) (exerciseUseCase.ArchiveImportPreview, error) {
	f.reimport.input = in
	f.reimport.actor = actor
	f.reimport.calls++
	return f.reimport.view, f.reimport.err
}
func (f *fakeUC) ListImportTargets(_ context.Context, actor exerciseUseCase.Actor, search string, cursor uuid.UUID, limit int) (pagination.CursorPage[exerciseUseCase.ImportTargetExercise], error) {
	f.reimport.actor = actor
	f.reimport.search = search
	f.reimport.cursor = cursor
	f.reimport.limit = limit
	f.reimport.calls++
	return f.reimport.targets, f.reimport.err
}

func TestArchivePreviewHTTPMapsOwnedSourceAndPerItemChoices(t *testing.T) {
	source, target, event := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{reimport: previewCalls{view: exerciseUseCase.ArchiveImportPreview{Items: []exerciseUseCase.ArchiveImportPreviewItem{{ItemKey: ".", Name: "Source task", Target: exerciseUseCase.ImportTarget{ItemKey: ".", Mode: "new"}, ConfirmationRevision: "hash"}}}}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/preview", `{"UploadID":"`+source.String()+`","Password":"private","OwnerEventID":"`+event.String()+`","FixedExerciseID":"`+target.String()+`","Targets":[{"ItemKey":".","Mode":"update","ExerciseID":"`+target.String()+`"}]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, jobCaller, uc.reimport.actor.UserID)
	require.Equal(t, source, uc.reimport.input.UploadID)
	require.Equal(t, "private", uc.reimport.input.Password)
	require.Equal(t, target, uc.reimport.input.FixedExerciseID)
	require.Equal(t, event, *uc.reimport.input.OwnerEventID)
	require.Equal(t, []exerciseUseCase.ImportTarget{{ItemKey: ".", Mode: "update", ExerciseID: target}}, uc.reimport.input.Targets)
	require.Contains(t, w.Body.String(), `"ConfirmationRevision":"hash"`)
	require.NotContains(t, w.Body.String(), "private")
	require.NotContains(t, w.Body.String(), "00000000-0000-0000-0000-000000000000")
}
func TestArchivePreviewHTTPPreservesTypedFieldErrors(t *testing.T) {
	uc := &fakeUC{reimport: previewCalls{err: exerciseModel.ErrImportTargetDuplicate.WithPublicContext("itemKey", "folder").WithPublicContext("field", "target").Err()}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/preview", `{"FileID":"`+uuid.Must(uuid.NewV7()).String()+`"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"itemKey":"folder"`)
	require.Contains(t, w.Body.String(), `"field":"target"`)
}
func TestArchiveTargetsHTTPDefaultsAndCursor(t *testing.T) {
	cursor := uuid.Must(uuid.NewV7())
	uc := &fakeUC{reimport: previewCalls{targets: pagination.NewCursorPage([]exerciseUseCase.ImportTargetExercise{}, false, uuid.Nil, 0)}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodGet, "/api/exercises/import/targets?search=web&cursor="+cursor.String(), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "web", uc.reimport.search)
	require.Equal(t, cursor, uc.reimport.cursor)
	require.Equal(t, 50, uc.reimport.limit)
	w = jsonRequest(newEngine(uc, jobCaller), http.MethodGet, "/api/exercises/import/targets?pageSize=0", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 50, uc.reimport.limit)
	for _, query := range []string{"pageSize=201", "cursor=invalid", "pageSize=-1", "sort=asc"} {
		count := uc.reimport.calls
		w = jsonRequest(newEngine(uc, jobCaller), http.MethodGet, "/api/exercises/import/targets?"+query, "")
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Equal(t, count, uc.reimport.calls)
	}
}
