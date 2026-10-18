package exercise_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
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
	running  []exerciseUseCase.ImportJobView
	jobErr   error
	// noRegistry is what RequireImageUploads answers; required counts the asks.
	noRegistry error
	required   int
	// uploadErr is what ImportUploadedArchive fails with.
	uploadErr error
}

func (f *fakeUC) QueueArchiveImport(_ context.Context, in exerciseUseCase.QueueImportInput) (uuid.UUID, error) {
	f.archiveImport.queued = append(f.archiveImport.queued, in)
	return f.archiveImport.jobID, f.err
}

func (f *fakeUC) ImportUploadedArchive(_ context.Context, in exerciseUseCase.ImportUploadInput) (exerciseUseCase.ArchiveImportStart, error) {
	f.archiveImport.uploaded = append(f.archiveImport.uploaded, in)
	return f.archiveImport.start, f.archiveImport.uploadErr
}

func (f *fakeUC) ArchiveImportJob(_ context.Context, id, user uuid.UUID) (exerciseUseCase.ImportJobView, error) {
	f.archiveImport.jobAsked = [2]uuid.UUID{id, user}
	return f.archiveImport.job, f.archiveImport.jobErr
}

func (f *fakeUC) RunningArchiveImports(_ context.Context, user uuid.UUID) ([]exerciseUseCase.ImportJobView, error) {
	f.archiveImport.jobAsked = [2]uuid.UUID{uuid.Nil, user}
	return f.archiveImport.running, f.archiveImport.jobErr
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

	uc.archiveImport.job = exerciseUseCase.ImportJobView{ID: jobID, State: exerciseModel.ImportJobRunning, Stage: exerciseModel.ImportStageImages, Progress: exerciseModel.ImportProgress{
		Stage: exerciseModel.ImportStageImages, ImagesDone: 3, ImagesTotal: 7, TasksDone: 0, TasksTotal: 12, BytesPushed: 300, BytesTotal: 700,
	}}
	w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), "")
	if w.Code != http.StatusOK || w.Body.String() == "" || strings.Contains(w.Body.String(), `"Error"`) || !strings.Contains(w.Body.String(), `"State":"running"`) {
		t.Fatalf("running: %d %s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"Stage":"images"`, `"ImagesDone":3`, `"ImagesTotal":7`, `"TasksDone":0`, `"TasksTotal":12`, `"BytesPushed":300`, `"BytesTotal":700`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("running progress lacks %s: %s", want, w.Body.String())
		}
	}

	uc.archiveImport.jobErr = exerciseModel.ErrImportJobNotFound.Err()
	if w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), ""); w.Code != http.StatusNotFound {
		t.Fatalf("not found: %d", w.Code)
	}
	if w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/nope", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}
}

// A task deleted since the import is left out of the list; any other lookup failure is an error, not a short list.
func TestImportJobStatus_ExerciseLookup(t *testing.T) {
	created := uuid.Must(uuid.NewV7())
	uc := &fakeUC{archiveImport: archiveImportCalls{job: exerciseUseCase.ImportJobView{ID: jobID, State: exerciseModel.ImportJobDone, ExerciseIDs: []uuid.UUID{created}}}}
	r := newEngine(uc, jobCaller)
	uc.err = exerciseModel.ErrExerciseNotFound.Err()
	w := jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"State":"done"`) || strings.Contains(w.Body.String(), `"Exercises"`) {
		t.Fatalf("deleted task: %d %s", w.Code, w.Body.String())
	}
	uc.err = model.ErrPlatform.WithError(errors.New("db down")).Err()
	if w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs/"+jobID.String(), ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("lookup failure: %d %s", w.Code, w.Body.String())
	}
}

// The multipart import of an archive with images asks for the registry before storing anything.
func TestImportMultipart_WithImagesNeedsTheRegistryFirst(t *testing.T) {
	uc := &fakeUC{archiveImport: archiveImportCalls{noRegistry: imageRegistryModel.ErrRegistryNotConfigured.Err()}}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("archive", "tasks.zip")
	_, _ = part.Write(imageArchive(t))
	_ = form.Close()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	newEngine(uc, jobCaller).ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "72801") || uc.uploadContent != "" || len(uc.archiveImport.queued) != 0 {
		t.Fatalf("status %d body %s stored %d bytes", w.Code, w.Body.String(), len(uc.uploadContent))
	}
}

// Import errors: an archive's own failure (a broken zip) keeps its text as a 400; anything else (storage, database)
// is the generic internal error, never its text.
func TestImportUpload_ErrorsNeverLeakInternals(t *testing.T) {
	uc := &fakeUC{archiveImport: archiveImportCalls{uploadErr: fmt.Errorf("open: %w", zip.ErrFormat)}}
	r := newEngine(uc, jobCaller)
	body := `{"UploadID":"` + uuid.Must(uuid.NewV7()).String() + `"}`
	if w := jsonRequest(r, http.MethodPost, "/api/exercises/import/upload", body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not a valid zip file") {
		t.Fatalf("broken zip: %d %s", w.Code, w.Body.String())
	}
	uc.archiveImport.uploadErr = errors.New("read media/uploads/0192f0a0/3: connection reset by s3.internal:9000")
	w := jsonRequest(r, http.MethodPost, "/api/exercises/import/upload", body)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "media/uploads") || strings.Contains(w.Body.String(), "s3.internal") {
		t.Fatalf("storage failure: %d %s", w.Code, w.Body.String())
	}
}

// archiveExportCalls records what the background export routes passed to the use case.
type archiveExportCalls struct {
	started  []exerciseUseCase.StartExportInput
	jobID    uuid.UUID
	startErr error
	job      exerciseUseCase.ExportJobView
	jobAsked [2]uuid.UUID
	jobErr   error
}

func (f *fakeUC) StartArchiveExport(_ context.Context, in exerciseUseCase.StartExportInput) (uuid.UUID, error) {
	f.archiveExport.started = append(f.archiveExport.started, in)
	return f.archiveExport.jobID, f.archiveExport.startErr
}

func (f *fakeUC) ArchiveExportJob(_ context.Context, id, user uuid.UUID) (exerciseUseCase.ExportJobView, error) {
	f.archiveExport.jobAsked = [2]uuid.UUID{id, user}
	return f.archiveExport.job, f.archiveExport.jobErr
}

// POST /exercises/export with IncludeImages runs in the background: 202 {JobID}, the zip is not built in the request.
func TestExport_WithImagesQueuedAnswers202(t *testing.T) {
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{Name: "Web lab"}, archiveExport: archiveExportCalls{jobID: jobID}}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/export", `{"IDs":["`+first.String()+`"],"IncludeImages":true}`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"JobID":"`+jobID.String()+`"`) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	want := exerciseUseCase.StartExportInput{IDs: []uuid.UUID{first}, FileName: "web-lab.cybericebox.zip", CreatedBy: jobCaller}
	if got := uc.archiveExport.started; len(got) != 1 || !sameExportInput(got[0], want) {
		t.Fatalf("passed %+v", got)
	}
	if uc.uploadName != "" {
		t.Fatal("nothing is stored by the request")
	}

	uc = &fakeUC{exerciseView: exerciseUseCase.ExerciseView{Name: "Web lab"}, archiveExport: archiveExportCalls{jobID: jobID}}
	w = jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/export", `{"IDs":["`+first.String()+`","`+second.String()+`"],"IncludeImages":true,"IncludeSecrets":true,"Password":"pw"}`)
	got := uc.archiveExport.started
	if w.Code != http.StatusAccepted || len(got) != 1 || got[0].FileName != "exercises.cybericebox.zip" || !got[0].IncludeSecrets || got[0].Password != "pw" || len(got[0].IDs) != 2 {
		t.Fatalf("status %d passed %+v", w.Code, got)
	}
}

func sameExportInput(a, b exerciseUseCase.StartExportInput) bool {
	return fmt.Sprint(a.IDs) == fmt.Sprint(b.IDs) && a.IncludeSecrets == b.IncludeSecrets && a.Password == b.Password && a.FileName == b.FileName && a.CreatedBy == b.CreatedBy
}

// Secrets still need a password, and an unknown task is refused, before any job is recorded.
func TestExport_WithImagesValidatesFirst(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	w := jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/export", `{"IDs":["`+id.String()+`"],"IncludeImages":true,"IncludeSecrets":true}`)
	if w.Code != http.StatusBadRequest || len(uc.archiveExport.started) != 0 {
		t.Fatalf("status %d", w.Code)
	}
	uc = &fakeUC{err: exerciseModel.ErrExerciseNotFound.Err()}
	w = jsonRequest(newEngine(uc, jobCaller), http.MethodPost, "/api/exercises/export", `{"IDs":["`+id.String()+`"],"IncludeImages":true}`)
	if w.Code != http.StatusNotFound || len(uc.archiveExport.started) != 0 {
		t.Fatalf("status %d", w.Code)
	}
}

// GET /exercises/export/jobs/{jobID}: running, done (the stored zip, read in chunks) and failed.
func TestExportJob_States(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	cases := map[string]struct {
		view exerciseUseCase.ExportJobView
		want []string
	}{
		"running": {exerciseUseCase.ExportJobView{ID: jobID, State: exerciseModel.ImportJobRunning}, []string{`"State":"running"`}},
		"done": {exerciseUseCase.ExportJobView{ID: jobID, State: exerciseModel.ImportJobDone, File: &mediaModel.File{ID: fileID, Name: "x.cybericebox.zip", SizeBytes: 12}},
			[]string{`"State":"done"`, `"FileID":"` + fileID.String() + `"`, `"Name":"x.cybericebox.zip"`, `"Size":12`, `"ChunkSize":5`, `"Chunks":3`}},
		"failed": {exerciseUseCase.ExportJobView{ID: jobID, State: exerciseModel.ImportJobFailed, Error: &exerciseModel.ImportJobError{Code: 72830, Message: "m", Context: map[string]any{"device": "web", "reason": "not_found"}}},
			[]string{`"State":"failed"`, `"Code":72830`, `"device":"web"`, `"reason":"not_found"`}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &fakeUC{chunkBytes: 5, archiveExport: archiveExportCalls{job: tc.view}}
			w := jsonRequest(newEngine(uc, jobCaller), http.MethodGet, "/api/exercises/export/jobs/"+jobID.String(), "")
			if w.Code != http.StatusOK {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			for _, part := range tc.want {
				if !strings.Contains(w.Body.String(), part) {
					t.Fatalf("missing %s in %s", part, w.Body.String())
				}
			}
			if uc.archiveExport.jobAsked != [2]uuid.UUID{jobID, jobCaller} {
				t.Fatalf("asked %v", uc.archiveExport.jobAsked)
			}
		})
	}
}

func TestExportJob_NotFoundAndBadID(t *testing.T) {
	uc := &fakeUC{archiveExport: archiveExportCalls{jobErr: exerciseModel.ErrExportJobNotFound.Err()}}
	if w := jsonRequest(newEngine(uc, jobCaller), http.MethodGet, "/api/exercises/export/jobs/"+jobID.String(), ""); w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	if w := jsonRequest(newEngine(uc, jobCaller), http.MethodGet, "/api/exercises/export/jobs/nope", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

// GET /exercises/import/jobs: the caller's running imports with their progress.
func TestRunningImportJobs(t *testing.T) {
	uc := &fakeUC{archiveImport: archiveImportCalls{running: []exerciseUseCase.ImportJobView{{
		ID: jobID, State: exerciseModel.ImportJobRunning, Stage: exerciseModel.ImportStageTasks,
		Progress: exerciseModel.ImportProgress{ImagesDone: 2, ImagesTotal: 2, TasksDone: 5, TasksTotal: 12},
	}}}}
	r := newEngine(uc, jobCaller)
	w := jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs", "")
	if w.Code != http.StatusOK || uc.archiveImport.jobAsked[1] != jobCaller {
		t.Fatalf("status %d asked %v", w.Code, uc.archiveImport.jobAsked)
	}
	for _, want := range []string{`"JobID":"` + jobID.String() + `"`, `"Stage":"tasks"`, `"TasksDone":5`, `"TasksTotal":12`, `"ImagesTotal":2`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("lacks %s: %s", want, w.Body.String())
		}
	}
	uc.archiveImport.running = nil
	if w = jsonRequest(r, http.MethodGet, "/api/exercises/import/jobs", ""); !strings.Contains(w.Body.String(), `"data":[]`) && !strings.Contains(w.Body.String(), `"Data":[]`) {
		t.Fatalf("empty list should be []: %s", w.Body.String())
	}
}
