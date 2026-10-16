package event_test

import (
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/gofrs/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewEveryEventRouteRejectsViewerAndUnassignedPlatformAdmin(t *testing.T) {
	actor, event, selection, task, hint, file := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := "/api/events/" + event.String() + "/manage/participant-preview"
	selected := "/selections/" + selection.String()
	taskPath := selected + "/tasks/" + task.String()
	routes := []struct{ method, path string }{
		{"GET", "/settings"}, {"PUT", "/settings"}, {"GET", "/catalog"}, {"GET", "/board"}, {"DELETE", "/runs"},
		{"POST", selected + "/start"}, {"DELETE", selected + "/run"}, {"GET", selected + "/lab"}, {"GET", selected + "/vpn"}, {"POST", selected + "/proxy"},
		{"POST", taskPath + "/submit"}, {"GET", taskPath + "/solves"}, {"POST", taskPath + "/hints/" + hint.String() + "/unlock"}, {"GET", taskPath + "/files/" + file.String()},
	}
	for _, route := range routes {
		t.Run(route.method+route.path, func(t *testing.T) {
			denied := &accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}
			router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: denied}, actor)
			req := httptest.NewRequest(route.method, base+route.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("viewer/admin bypass: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
