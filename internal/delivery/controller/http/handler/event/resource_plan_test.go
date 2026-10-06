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
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type resourcePlanUC struct {
	eventHandler.IUseCase
	plan      eventUseCase.EventResourcePlan
	exercises []eventUseCase.EventExerciseView
	catalog   []eventUseCase.PublishedExerciseChoice
}

func (u *resourcePlanUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (u *resourcePlanUC) GetResourcePlan(context.Context, uuid.UUID) (eventUseCase.EventResourcePlan, error) {
	return u.plan, nil
}
func (u *resourcePlanUC) ListEventExercises(context.Context, uuid.UUID) ([]eventUseCase.EventExerciseView, error) {
	return u.exercises, nil
}
func (u *resourcePlanUC) ListPublishedExercisesForEvent(context.Context, uuid.UUID, string, string, []string) ([]eventUseCase.PublishedExerciseChoice, error) {
	return u.catalog, nil
}

func totals(devices int, cpu, mem int64) resourcesModel.Totals {
	return resourcesModel.Totals{Devices: devices, Amount: resourcesModel.Amount{CPUMillicores: cpu, MemoryBytes: mem}}
}

func TestEventResourcePlanRouteShowsTasksGroupOverheadAndTotal(t *testing.T) {
	actor, eventID, exerciseID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &resourcePlanUC{plan: eventUseCase.EventResourcePlan{
		Tasks:     []eventUseCase.PlanTask{{ExerciseID: exerciseID, ExerciseName: "Web", Range: resourcesModel.Range{Min: totals(1, 25, 64<<20), Max: totals(2, 275, 1<<30)}, Reserved: totals(2, 275, 1<<30), Heavy: true, InternetLab: true}},
		TeamTasks: totals(2, 275, 1<<30),
		Group:     eventUseCase.GroupOverhead{MaxUsers: 4, InternetLabs: 1, VPN: resourcesModel.Amount{CPUMillicores: 30, MemoryBytes: 48 << 20}, Known: true},
		PerTeam:   totals(2, 305, 1<<30+48<<20), Teams: 10, TeamsBasis: "max_teams", Total: totals(20, 3050, 10*(1<<30+48<<20)),
	}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/resource-plan", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Tasks []struct {
				ExerciseName  string
				Range         struct{ Min, Max struct{ Devices int } }
				Reserved      struct{ CPUMillicores int64 }
				ResourceHeavy bool
			}
			Group struct {
				MaxUsers int
				VPN      struct{ CPUMillicores int64 }
				Known    bool
			}
			PerTeam    struct{ CPUMillicores int64 }
			Teams      int
			TeamsBasis string
			Total      struct{ CPUMillicores int64 }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	if len(d.Tasks) != 1 || d.Tasks[0].Range.Min.Devices != 1 || d.Tasks[0].Range.Max.Devices != 2 || d.Tasks[0].Reserved.CPUMillicores != 275 || !d.Tasks[0].ResourceHeavy {
		t.Fatalf("tasks: %s", w.Body.String())
	}
	if d.Group.MaxUsers != 4 || d.Group.VPN.CPUMillicores != 30 || !d.Group.Known || d.PerTeam.CPUMillicores != 305 || d.Teams != 10 || d.TeamsBasis != "max_teams" || d.Total.CPUMillicores != 3050 {
		t.Fatalf("plan: %s", w.Body.String())
	}
}

// Authors and organizers never learn the name of a laboratory: no event or exercise response carries one.
func TestEventResponsesNeverNameALaboratory(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &resourcePlanUC{
		exercises: []eventUseCase.EventExerciseView{{ID: uuid.Must(uuid.NewV7()), ExerciseName: "Web", ResourceHeavy: true, NoAgentFits: true, Resources: resourcesModel.Range{Max: totals(1, 25, 64<<20)}}},
		catalog:   []eventUseCase.PublishedExerciseChoice{{ID: uuid.Must(uuid.NewV7()), Name: "Web", ResourceHeavy: true, Resources: resourcesModel.Range{Max: totals(1, 25, 64<<20)}}},
		plan:      eventUseCase.EventResourcePlan{NoAgentFits: true, Tasks: []eventUseCase.PlanTask{{ExerciseName: "Web", NoAgentFits: true}}},
	}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	for _, path := range []string{"exercises", "exercise-catalog", "resource-plan"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/"+path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
		for _, banned := range []string{`"Agent"`, `"Agents"`, `"Warnings"`, `"Fit"`} {
			if strings.Contains(w.Body.String(), banned) {
				t.Errorf("%s names a laboratory (%s): %s", path, banned, w.Body.String())
			}
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/exercises", nil))
	if !strings.Contains(w.Body.String(), `"ResourceHeavy":true`) || !strings.Contains(w.Body.String(), `"NoAgentFits":true`) || !strings.Contains(w.Body.String(), `"Resources":{"Min"`) {
		t.Errorf("the attached task carries its resources and marks: %s", w.Body.String())
	}
}
