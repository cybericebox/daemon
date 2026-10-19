package exercise_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

type bulkBody struct {
	Data struct {
		Changed int
		Skipped int
		Items   []struct {
			ID       uuid.UUID
			Exercise *struct {
				ID         uuid.UUID
				ArchivedAt *string
			}
			Code int
		}
	}
}

func idsJSON(ids ...uuid.UUID) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// Bulk archive: any signed-in user reaches the per-task rule (PermSelf, as for one task); the answer counts the
// changed and skipped tasks and names the reason of each skipped one by its error code, an internal failure only as
// the generic internal code.
func TestBulkArchive_PerItemResults(t *testing.T) {
	changed, refused, broken := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{bulkResults: []exerciseUseCase.BulkResult{
		{ID: changed, View: exerciseUseCase.ExerciseView{ID: changed}},
		{ID: refused, Err: exerciseModel.ErrExerciseForbidden.Err()},
		{ID: broken, Err: model.ErrPlatform.WithError(errors.New("db down")).WithMessage("Failed to get exercise").Err()},
	}}
	w := jsonRequest(engineAs(uc, rbac.RoleUser), http.MethodPost, "/api/exercises/bulk/archive", `{"IDs":`+idsJSON(changed, refused, broken)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if uc.bulkArchived == nil || !*uc.bulkArchived || len(uc.bulkIDs) != 3 {
		t.Fatalf("use case got archived=%v ids=%v", uc.bulkArchived, uc.bulkIDs)
	}
	if strings.Contains(w.Body.String(), "db down") {
		t.Fatal("an internal failure leaked its text")
	}
	var body bulkBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items := body.Data.Items
	if body.Data.Changed != 1 || body.Data.Skipped != 2 || len(items) != 3 {
		t.Fatalf("summary %+v", body.Data)
	}
	if items[0].Exercise == nil || items[0].Exercise.ID != changed || items[0].Code != 0 {
		t.Fatalf("changed item %+v", items[0])
	}
	if items[1].Exercise != nil || items[1].Code != exerciseModel.ErrExerciseForbidden.Err().StatusCode().FullCode() {
		t.Fatalf("refused item %+v", items[1])
	}
	if items[2].Exercise != nil || items[2].Code != model.ErrPlatform.Err().StatusCode().FullCode() {
		t.Fatalf("broken item %+v", items[2])
	}
}

// Bulk unarchive goes to the same use case with archived=false.
func TestBulkUnarchive_ReachesUseCase(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	uc := &fakeUC{bulkResults: []exerciseUseCase.BulkResult{{ID: id, View: exerciseUseCase.ExerciseView{ID: id}}}}
	w := jsonRequest(engineAs(uc, rbac.RoleUser), http.MethodPost, "/api/exercises/bulk/unarchive", `{"IDs":`+idsJSON(id)+`}`)
	if w.Code != http.StatusOK || uc.bulkArchived == nil || *uc.bulkArchived {
		t.Fatalf("status %d archived=%v", w.Code, uc.bulkArchived)
	}
}

// The batch is capped (MaxBulkExercises) and must not be empty: nothing reaches the use case.
func TestBulkArchive_BatchLimits(t *testing.T) {
	over := make([]uuid.UUID, exerciseUseCase.MaxBulkExercises+1)
	for i := range over {
		over[i] = uuid.Must(uuid.NewV7())
	}
	for name, ids := range map[string]string{"empty": `[]`, "too many": idsJSON(over...), "missing": `null`} {
		t.Run(name, func(t *testing.T) {
			uc := &fakeUC{}
			w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPost, "/api/exercises/bulk/archive", `{"IDs":`+ids+`}`)
			if w.Code != http.StatusBadRequest || uc.bulkIDs != nil {
				t.Fatalf("status %d reached=%v", w.Code, uc.bulkIDs != nil)
			}
		})
	}
	uc := &fakeUC{}
	full := over[:exerciseUseCase.MaxBulkExercises]
	if w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPost, "/api/exercises/bulk/archive", `{"IDs":`+idsJSON(full...)+`}`); w.Code != http.StatusOK {
		t.Fatalf("a full batch: status %d", w.Code)
	}
}

// Bulk access has the gate of one task's access (exercises.write): an event viewer or a read-only admin is refused
// before anything changes; an admin's level and events reach the use case; a bad level is a bad request.
func TestBulkAccess_GateAndInput(t *testing.T) {
	id, event := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	body := `{"IDs":` + idsJSON(id) + `,"AccessLevel":"selected","EventIDs":["` + event.String() + `"]}`
	for _, role := range []rbac.Role{rbac.RoleUser, rbac.RoleAdminViewer} {
		uc := &fakeUC{}
		if w := jsonRequest(engineAs(uc, role), http.MethodPut, "/api/exercises/bulk/access", body); w.Code != http.StatusForbidden || uc.bulkAccess != nil {
			t.Fatalf("%s: status %d reached=%v", role, w.Code, uc.bulkAccess != nil)
		}
	}
	uc := &fakeUC{bulkResults: []exerciseUseCase.BulkResult{{ID: id, View: exerciseUseCase.ExerciseView{ID: id}}}}
	if w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPut, "/api/exercises/bulk/access", body); w.Code != http.StatusOK {
		t.Fatalf("admin: status %d body %s", w.Code, w.Body.String())
	}
	if in := uc.bulkAccess; in == nil || in.AccessLevel != exerciseModel.AccessSelectedEvents || len(in.EventIDs) != 1 || in.EventIDs[0] != event || in.UpdatedBy != jobCaller {
		t.Fatalf("access input %+v", uc.bulkAccess)
	}
	uc = &fakeUC{}
	if w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPut, "/api/exercises/bulk/access", `{"IDs":`+idsJSON(id)+`,"AccessLevel":"everyone"}`); w.Code != http.StatusBadRequest || uc.bulkAccess != nil {
		t.Fatalf("bad level: status %d", w.Code)
	}
}

// Access requests name at most 500 events, on one task's route and on the bulk route: more is a bad request and
// nothing reaches the use case; exactly 500 passes.
func TestAccess_EventIDsCapped(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	events := make([]uuid.UUID, 501)
	for i := range events {
		events[i] = uuid.Must(uuid.NewV7())
	}
	body := func(prefix string, n int) string {
		return `{` + prefix + `"AccessLevel":"selected","EventIDs":` + idsJSON(events[:n]...) + `}`
	}
	for _, route := range []struct{ url, prefix string }{
		{"/api/exercises/" + id.String() + "/access", ""},
		{"/api/exercises/bulk/access", `"IDs":` + idsJSON(id) + `,`},
	} {
		uc := &fakeUC{}
		if w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPut, route.url, body(route.prefix, 501)); w.Code != http.StatusBadRequest || uc.bulkAccess != nil || uc.accessInput.EventIDs != nil {
			t.Fatalf("%s with 501 events: status %d", route.url, w.Code)
		}
		uc = &fakeUC{bulkResults: []exerciseUseCase.BulkResult{{ID: id, View: exerciseUseCase.ExerciseView{ID: id}}}}
		if w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPut, route.url, body(route.prefix, 500)); w.Code != http.StatusOK {
			t.Fatalf("%s with 500 events: status %d body %s", route.url, w.Code, w.Body.String())
		}
	}
}

// An over-the-cap refusal of the use case (a caller that skipped the route cap) answers as an error, not a result.
func TestBulkArchive_UseCaseRefusal(t *testing.T) {
	uc := &fakeUC{err: model.ErrPlatform.WithMessage("at most 200").Err()}
	if w := jsonRequest(engineAs(uc, rbac.RoleAdmin), http.MethodPost, "/api/exercises/bulk/archive", `{"IDs":`+idsJSON(uuid.Must(uuid.NewV7()))+`}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}
