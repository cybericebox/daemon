package exercise_test

import (
	"bytes"
	"context"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/pagination"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArchiveCommitHTTPBindsChoicesAndReturnsCountsWithoutChangingLegacyArray(t *testing.T) {
	source, target := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, route := range []string{"upload", "uploaded"} {
		field := "UploadID"
		if route == "uploaded" {
			field = "FileID"
		}
		uc := &fakeUC{archiveImport: archiveImportCalls{start: exerciseUseCase.ArchiveImportStart{Created: 0, Updated: 1, Results: []exerciseModel.ImportResult{{ItemKey: ".", Mode: "update", ExerciseID: target}}}}}
		w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/"+route, `{"`+field+`":"`+source.String()+`","FixedExerciseID":"`+target.String()+`","Targets":[{"ItemKey":".","Mode":"update","ExerciseID":"`+target.String()+`"}],"ConfirmedUpdates":[{"ItemKey":".","Revision":"exact-preview","ReplaceDraft":true}]}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Len(t, uc.archiveImport.commits, 1)
		in := uc.archiveImport.commits[0]
		require.Equal(t, target, in.FixedExerciseID)
		require.Equal(t, "exact-preview", in.ConfirmedUpdates[0].Revision)
		require.True(t, in.ConfirmedUpdates[0].ReplaceDraft)
		require.Contains(t, w.Body.String(), `"Updated":1`)
		require.Contains(t, w.Body.String(), `"Created":0`)
		require.Contains(t, w.Body.String(), `"Results":[`)
		w = jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/"+route, `{"`+field+`":"`+source.String()+`"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"Data":[]`)
	}
}

func TestRawMultipartUpdateIsRefusedBeforeAnyImportOrStorage(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, err := writer.CreateFormFile("archive", "source.zip")
		require.NoError(t, err)
		_, err = file.Write([]byte("archive bytes are not interpreted for an explicit update"))
		require.NoError(t, err)
		if fixed {
			require.NoError(t, writer.WriteField("FixedExerciseID", uuid.Must(uuid.NewV7()).String()))
		} else {
			require.NoError(t, writer.WriteField("Targets", `[{"ItemKey":"folder","Mode":"update","ExerciseID":"`+uuid.Must(uuid.NewV7()).String()+`"}]`))
		}
		require.NoError(t, writer.Close())
		uc := &fakeUC{}
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/import", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		newEngine(uc, jobCaller).ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"field":"target"`)
		require.Empty(t, uc.imports)
		require.Empty(t, uc.archiveImport.queued)
	}
}

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
