package exercise_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// postArchive sends archive bytes to the multipart import route.
func postArchive(t *testing.T, archive []byte) (*httptest.ResponseRecorder, response.Response) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("archive", "tasks.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	r := newEngine(&fakeUC{}, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"))
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out response.Response
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return w, out
}

func TestImportArchive_StrayFileKeepsCodeAndContext(t *testing.T) {
	archive, err := exerciseUseCase.BuildArchiveNoManifest(map[string][]byte{
		"a/exercise.yaml": []byte("schema: cib-exercise/1\n"),
		"readme.md":       []byte("r"),
	})
	if err != nil {
		t.Fatal(err)
	}
	w, out := postArchive(t, archive)
	want := exerciseModel.ErrArchiveFileUnlinked.Err().StatusCode()
	if w.Code != http.StatusBadRequest || out.Status.Code != want.FullCode() || want.FullCode() != 20982 {
		t.Fatalf("status %d code %d, want 400 / 20982: %s", w.Code, out.Status.Code, w.Body.String())
	}
	if out.Status.Context["file"] != "readme.md" {
		t.Fatalf("context = %v", out.Status.Context)
	}
}

func TestImportArchive_BrokenZipStaysBadRequest(t *testing.T) {
	w, _ := postArchive(t, []byte("not a zip"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
	}
}
