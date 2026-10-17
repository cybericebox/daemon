package exercise_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// Two exercises with one name export as one zip: folders Веб/ and Веб_2/, one root manifest.
func TestExport_SeveralTasksOneZip(t *testing.T) {
	single, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	uc := &fakeUC{exportedArchive: single, exerciseView: exerciseUseCase.ExerciseView{Name: "Веб"}, chunkBytes: 128}
	r := newEngine(uc, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"))
	body, _ := json.Marshal(map[string]any{"IDs": []string{
		"0192f0a0-0000-7000-8000-000000000001", "0192f0a0-0000-7000-8000-000000000002",
	}})
	_, bundle := exportAndDownload(t, r, string(body))
	files, err := exerciseUseCase.ReadArchiveV1(bytes.NewReader(bundle), exerciseModel.DefaultArchiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["Веб/exercise.json"] == nil || files["Веб_2/exercise.json"] == nil {
		t.Fatalf("entries: %v", keys(files))
	}
	imports, err := exerciseUseCase.ExpandExerciseArchiveBundle(bundle, "", exerciseModel.DefaultArchiveLimits())
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
