package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// bulkUC answers the bulk actions; the first ID is applied, every other one is refused like a single detach.
type bulkUC struct {
	eventHandler.IUseCase
	readErr, manageErr error
	calls              []string
	override           *eventModel.ScoringProfile
	confirmed          bool
}

func (u *bulkUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error   { return u.readErr }
func (u *bulkUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error { return u.manageErr }

func (u *bulkUC) results(ids []uuid.UUID) []eventUseCase.BulkExerciseResult {
	out := make([]eventUseCase.BulkExerciseResult, 0, len(ids))
	for i, id := range ids {
		var err error
		if i > 0 {
			err = eventExerciseModel.ErrEventExerciseDetachNeedsConfirm.Err()
		}
		out = append(out, eventUseCase.BulkExerciseResult{ID: id, Err: err})
	}
	return out
}

func (u *bulkUC) BulkSetEventExerciseVisibility(_ context.Context, _ uuid.UUID, ids []uuid.UUID, published bool) ([]eventUseCase.BulkExerciseResult, error) {
	u.calls = append(u.calls, "visibility")
	return u.results(ids), nil
}

func (u *bulkUC) BulkSetEventExerciseScoring(_ context.Context, _ uuid.UUID, ids []uuid.UUID, override *eventModel.ScoringProfile) ([]eventUseCase.BulkExerciseResult, error) {
	u.calls, u.override = append(u.calls, "scoring"), override
	return u.results(ids), nil
}

func (u *bulkUC) BulkDetachEventExercises(_ context.Context, _ uuid.UUID, ids []uuid.UUID, confirmed bool, _ uuid.UUID) ([]eventUseCase.BulkExerciseResult, error) {
	u.calls, u.confirmed = append(u.calls, "detach"), confirmed
	if len(ids) == 0 {
		return nil, eventExerciseModel.ErrEventExerciseBulkSize.Err()
	}
	return u.results(ids), nil
}

func TestBulkExerciseActionsReportEachItemAndRespectRoles(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ids := `"ExerciseIDs":["` + a.String() + `","` + b.String() + `"]`
	base := "/api/events/" + eventID.String() + "/manage/exercises/bulk/"
	routes := []struct{ method, path, body, call string }{
		{http.MethodPut, "visibility", `{` + ids + `,"Published":true}`, "visibility"},
		{http.MethodPut, "scoring", `{` + ids + `,"Override":{"Mode":0,"MinPoints":0,"MaxPoints":0,"FloorAtPercent":0}}`, "scoring"},
		{http.MethodPost, "detach", `{` + ids + `,"Confirm":true}`, "detach"},
	}
	for _, role := range []struct {
		name      string
		readErr   error
		manageErr error
		status    int
	}{
		{"manager (owner, write moderator or platform admin)", nil, nil, http.StatusOK},
		{"viewer", nil, eventManagerModel.ErrEventManagementForbidden.Err(), http.StatusForbidden},
		{"not a member", eventManagerModel.ErrEventManagementForbidden.Err(), eventManagerModel.ErrEventManagementForbidden.Err(), http.StatusForbidden},
	} {
		for _, route := range routes {
			t.Run(role.name+" "+route.path, func(t *testing.T) {
				u := &bulkUC{readErr: role.readErr, manageErr: role.manageErr}
				router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(route.method, base+route.path, strings.NewReader(route.body)))
				if w.Code != role.status {
					t.Fatalf("status = %d, want %d: %s", w.Code, role.status, w.Body.String())
				}
				if role.status != http.StatusOK {
					if len(u.calls) != 0 {
						t.Fatalf("a refused role reached the use case: %v", u.calls)
					}
					return
				}
				var body struct {
					Data struct {
						Changed, Skipped int
						Results          []struct {
							ID      uuid.UUID
							Changed bool
							Code    int
							Message string
						}
					}
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				d := body.Data
				if len(u.calls) != 1 || u.calls[0] != route.call || d.Changed != 1 || d.Skipped != 1 || len(d.Results) != 2 {
					t.Fatalf("calls %v, body %s", u.calls, w.Body.String())
				}
				if !d.Results[0].Changed || d.Results[1].Changed || d.Results[1].Code == 0 || d.Results[1].Message == "" {
					t.Fatalf("results = %+v", d.Results)
				}
				if route.call == "scoring" && (u.override == nil || u.override.Mode != eventModel.ScoringMode(0)) {
					t.Fatalf("override = %+v", u.override)
				}
				if route.call == "detach" && !u.confirmed {
					t.Fatal("Confirm was not passed")
				}
			})
		}
	}
}

func TestBulkExerciseActionsRefuseAnEmptySelection(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &bulkUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/manage/exercises/bulk/detach", strings.NewReader(`{"ExerciseIDs":[]}`)))
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}
