package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// archiveImportCalls records what the background import routes passed to the use case.
type archiveImportCalls struct {
	queued   []exerciseUseCase.QueueImportInput
	jobID    uuid.UUID
	uploaded []exerciseUseCase.ImportUploadInput
	start    exerciseUseCase.ArchiveImportStart
	job      exerciseUseCase.ImportJobView
	jobAsked [2]uuid.UUID
}

func (f *fakeUC) QueueArchiveImport(_ context.Context, in exerciseUseCase.QueueImportInput) (uuid.UUID, error) {
	f.archiveImport.queued = append(f.archiveImport.queued, in)
	return f.archiveImport.jobID, f.err
}

func (f *fakeUC) ImportUploadedArchive(_ context.Context, in exerciseUseCase.ImportUploadInput) (exerciseUseCase.ArchiveImportStart, error) {
	f.archiveImport.uploaded = append(f.archiveImport.uploaded, in)
	return f.archiveImport.start, f.err
}

func (f *fakeUC) ArchiveImportJob(_ context.Context, id, user uuid.UUID) (exerciseUseCase.ImportJobView, error) {
	f.archiveImport.jobAsked = [2]uuid.UUID{id, user}
	return f.archiveImport.job, f.err
}

var (
	jobCaller = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa")
	jobID     = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000c1")
)

// imageArchive is a task archive naming an image file.
func imageArchive(t *testing.T) []byte {
	t.Helper()
	b, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{
		"exercise.json":  []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Images"},"versions":[{"status":"draft","variants":[{"topology":{"devices":[{"name":"web","type":"container","image":{"file":"images/web.tar"}}]}}]}]}`),
		"images/web.tar": []byte("tar bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func jsonRequest(r http.Handler, method, url, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// POST /exercises/import/upload: an archive with image files answers 202 {JobID}.
func TestImportUpload_QueuedAnswers202(t *testing.T) {
	uploadID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{archiveImport: archiveImportCalls{start: exerciseUseCase.ArchiveImportStart{JobID: jobID}}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/upload", `{"UploadID":"`+uploadID.String()+`","Password":"pw"}`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"JobID":"`+jobID.String()+`"`) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if got := uc.archiveImport.uploaded; len(got) != 1 || got[0] != (exerciseUseCase.ImportUploadInput{UploadID: uploadID, Password: "pw", CreatedBy: jobCaller}) {
		t.Fatalf("passed %+v", got)
	}
}

// Without image files the tasks are created at once: 200 with the list.
func TestImportUpload_SynchronousAnswersTheTasks(t *testing.T) {
	created := exerciseUseCase.ExerciseView{ID: uuid.Must(uuid.NewV7()), Name: "Plain"}
	uc := &fakeUC{archiveImport: archiveImportCalls{start: exerciseUseCase.ArchiveImportStart{Exercises: []exerciseUseCase.ExerciseView{created}}}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/upload", `{"UploadID":"`+uuid.Must(uuid.NewV7()).String()+`"}`)
	var out struct{ Data []map[string]any }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data) != 1 || out.Data[0]["ID"] != created.ID.String() {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestImportUpload_BadBody(t *testing.T) {
	uc := &fakeUC{}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/upload", `{}`)
	if w.Code != http.StatusBadRequest || len(uc.archiveImport.uploaded) != 0 {
		t.Fatalf("status %d", w.Code)
	}
}

// POST /exercises/import/uploaded (a stored file): an archive naming image files is queued from that file.
func TestImportUploadedFile_WithImagesIsQueued(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		streamedFile:    mediaModel.File{ID: fileID, CreatedBy: uuid.NullUUID{UUID: jobCaller, Valid: true}},
		streamedContent: string(imageArchive(t)),
		archiveImport:   archiveImportCalls{jobID: jobID},
	}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/import/uploaded", `{"FileID":"`+fileID.String()+`"}`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), jobID.String()) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	want := exerciseUseCase.QueueImportInput{Source: exerciseModel.ImportSourceFile, SourceID: fileID, CreatedBy: jobCaller}
	if got := uc.archiveImport.queued; len(got) != 1 || got[0] != want {
		t.Fatalf("queued %+v", got)
	}
}

// The multipart import of an archive naming image files stores it as a file of the caller and queues it.
func TestImportMultipart_WithImagesIsStoredAndQueued(t *testing.T) {
	stored := uuid.Must(uuid.NewV7())
	uc := &fakeUC{uploadedFile: mediaModel.File{ID: stored}, archiveImport: archiveImportCalls{jobID: jobID}}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("archive", "tasks.zip")
	_, _ = part.Write(imageArchive(t))
	_ = form.WriteField("password", "pw")
	_ = form.Close()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	newEngine(uc, jobCaller).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), jobID.String()) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if uc.uploadCreatedBy != jobCaller || uc.uploadContent != string(imageArchive(t)) {
		t.Fatalf("stored by %s, %d bytes", uc.uploadCreatedBy, len(uc.uploadContent))
	}
	want := exerciseUseCase.QueueImportInput{Source: exerciseModel.ImportSourceFile, SourceID: stored, Password: "pw", CreatedBy: jobCaller}
	if got := uc.archiveImport.queued; len(got) != 1 || got[0] != want {
		t.Fatalf("queued %+v", got)
	}
}

// GET /exercises/import/jobs/:id: the caller's job; done lists the created tasks, failed carries the error.
func TestImportJobStatus(t *testing.T) {
	created := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		exerciseView:  exerciseUseCase.ExerciseView{ID: created, Name: "Images"},
		archiveImport: archiveImportCalls{job: exerciseUseCase.ImportJobView{ID: jobID, State: exerciseModel.ImportJobDone, ExerciseIDs: []uuid.UUID{created}}},
	}
	r := newEngine(uc, jobCaller)
	w := jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), "")
	var out struct {
		Data struct {
			State     string
			Exercises []map[string]any
			Error     map[string]any
		}
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.State != "done" || len(out.Data.Exercises) != 1 || out.Data.Exercises[0]["ID"] != created.String() || out.Data.Error != nil {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if uc.archiveImport.jobAsked != [2]uuid.UUID{jobID, jobCaller} {
		t.Fatalf("asked %v", uc.archiveImport.jobAsked)
	}

	uc.archiveImport.job = exerciseUseCase.ImportJobView{ID: jobID, State: exerciseModel.ImportJobFailed, Error: &exerciseModel.ImportJobError{
		Code: 72802, Message: "Image uploads are unavailable right now", Context: map[string]any{"reason": "unauthorized", "file": "images/web.tar"},
	}}
	w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"State":"failed"`) || !strings.Contains(w.Body.String(), `"Code":72802`) ||
		!strings.Contains(w.Body.String(), `"file":"images/web.tar"`) || strings.Contains(w.Body.String(), `"Exercises"`) {
		t.Fatalf("failed: %d %s", w.Code, w.Body.String())
	}

	uc.archiveImport.job = exerciseUseCase.ImportJobView{ID: jobID, State: exerciseModel.ImportJobRunning}
	w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), "")
	if w.Code != http.StatusOK || w.Body.String() == "" || strings.Contains(w.Body.String(), `"Error"`) || !strings.Contains(w.Body.String(), `"State":"running"`) {
		t.Fatalf("running: %d %s", w.Code, w.Body.String())
	}

	uc.err = exerciseModel.ErrImportJobNotFound.Err()
	if w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), ""); w.Code != http.StatusNotFound {
		t.Fatalf("not found: %d", w.Code)
	}
	if w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/nope", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}
}
