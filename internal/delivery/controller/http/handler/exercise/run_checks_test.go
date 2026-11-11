package exercise_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/pkg/err"
)

func TestRunDeployChecks_AsksForTheTaskOfTheDeploy(t *testing.T) {
	uc := &fakeUC{}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	deploy, task := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+deploy.String()+"/tasks/"+task.String()+"/run-checks", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Asked":true`) || uc.runChecks != deploy.String()+"/"+task.String() {
		t.Fatalf("code=%d body=%s asked=%q", w.Code, w.Body.String(), uc.runChecks)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control = %q", got)
	}

	bad := httptest.NewRecorder()
	r.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+deploy.String()+"/tasks/nope/run-checks", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad task id: %d", bad.Code)
	}
}

func TestRunDeployChecks_TooOftenIs429WithRetryAfter(t *testing.T) {
	uc := &fakeUC{err: exerciseModel.ErrChecksRunTooOften.WithDetail(err.DetailRetryAfterSeconds, int64(42)).Err()}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String()+"/tasks/"+uuid.Must(uuid.NewV7()).String()+"/run-checks", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "42" {
		t.Fatalf("Retry-After = %q", got)
	}
}
