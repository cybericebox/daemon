package exercise_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

func smallLimits(archiveBytes int64, entries int) *exerciseModel.ArchiveLimits {
	lim := exerciseModel.DefaultArchiveLimits()
	lim.ArchiveBytes, lim.Entries = archiveBytes, entries
	return &lim
}

// The import takes the archive limits in force once and hands the same set to every task it imports.
func TestImportArchive_ReadsTheLimitsOnceAndPassesThem(t *testing.T) {
	archive, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{
		"a/exercise.json": []byte(`{"schema":"cib-exercise/1"}`),
		"b/exercise.json": []byte(`{"schema":"cib-exercise/1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	uc := &fakeUC{limits: smallLimits(1<<20, 50)}
	w, _ := postArchiveTo(t, uc, archive)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if uc.limitReads != 1 || len(uc.imports) != 2 {
		t.Fatalf("limits read %d times, %d imports", uc.limitReads, len(uc.imports))
	}
	for _, in := range uc.imports {
		if in.Limits != *uc.limits {
			t.Fatalf("an import got other limits: %+v", in.Limits)
		}
	}
}

func TestImportArchive_OverTheSavedArchiveCap(t *testing.T) {
	archive, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	uc := &fakeUC{limits: smallLimits(int64(len(archive))-1, 1000)}
	w, out := postArchiveTo(t, uc, archive)
	if w.Code == http.StatusOK || out.Status.Code != exerciseModel.ErrArchiveTooLarge.Err().StatusCode().FullCode() || len(uc.imports) != 0 {
		t.Fatalf("an archive one byte over the saved cap: %d %s", w.Code, w.Body.String())
	}
}

// Several tasks of one export share one set of limits.
func TestExport_SeveralTasksShareOneLimitSet(t *testing.T) {
	single, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	uc := &fakeUC{exportedArchive: single, exerciseView: exerciseUseCase.ExerciseView{Name: "Веб"}, chunkBytes: 128, limits: smallLimits(1<<20, 50)}
	r := newEngine(uc, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa"))
	exportAndDownload(t, r, `{"IDs":["0192f0a0-0000-7000-8000-000000000001","0192f0a0-0000-7000-8000-000000000002"]}`)
	if uc.limitReads != 1 || len(uc.exportLimits) != 2 || uc.exportLimits[0] != *uc.limits || uc.exportLimits[1] != *uc.limits {
		t.Fatalf("limits read %d times, exports got %+v", uc.limitReads, uc.exportLimits)
	}
}

// An upload over the saved archive cap may only carry images: without the registry it is refused before it starts.
func TestChunkedUpload_SavedArchiveCapDecidesTheRegistryCheck(t *testing.T) {
	uc := &chunkedUC{upload: sampleUpload(), noRegistry: imageRegistryModel.ErrRegistryNotConfigured.Err()}
	uc.limits = smallLimits(1<<20, 1000)
	r := newChunkedEngine(uc, uuid.Must(uuid.NewV7()))
	start := func(size int64) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/uploads", strings.NewReader(fmt.Sprintf(`{"Name":"tasks.zip","Size":%d,"Purpose":"exercise-archive"}`, size)))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := start(1<<20 + 1); code != http.StatusConflict || uc.started[0] != nil {
		t.Fatalf("over the saved cap without a registry: %d", code)
	}
	if code := start(1 << 20); code != http.StatusOK || uc.purpose != mediaModel.UploadPurposeExerciseArchive {
		t.Fatalf("at the saved cap: %d", code)
	}
}
