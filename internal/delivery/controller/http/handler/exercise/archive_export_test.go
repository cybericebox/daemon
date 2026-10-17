package exercise_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"

	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// Two exercises with one name export as one zip: folders Веб/ and Веб_2/, one root manifest.
func TestExport_SeveralTasksOneZip(t *testing.T) {
	single, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	uc := &fakeUC{exportedArchive: single, exerciseView: exerciseUseCase.ExerciseView{Name: "Веб"}}
	r := newEngine(uc, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"))
	body, _ := json.Marshal(map[string]any{"IDs": []string{
		"0192f0a0-0000-7000-8000-000000000001", "0192f0a0-0000-7000-8000-000000000002",
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/export", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	files, err := exerciseUseCase.ReadArchiveV1(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["Веб/exercise.json"] == nil || files["Веб_2/exercise.json"] == nil {
		t.Fatalf("entries: %v", keys(files))
	}
	imports, err := exerciseUseCase.ExpandExerciseArchiveBundle(w.Body.Bytes(), "")
	if err != nil || len(imports) != 2 {
		t.Fatalf("bundle: %d imports, %v", len(imports), err)
	}
}

func keys(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	return out
}
