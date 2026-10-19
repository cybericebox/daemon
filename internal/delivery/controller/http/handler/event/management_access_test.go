package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type accessContractUC struct {
	eventHandler.IUseCase
	readErr   error
	manageErr error
}

func (u *accessContractUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return u.readErr
}

func (u *accessContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return u.manageErr
}

func TestManagementAccessReportsViewerAndManagerWithoutOpeningOtherEvents(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	url := "/api/events/" + eventID.String() + "/manage/access"
	for _, tc := range []struct {
		name      string
		readErr   error
		manageErr error
		status    int
		canManage bool
	}{
		{name: "manager", status: http.StatusOK, canManage: true},
		{name: "viewer", manageErr: eventManagerModel.ErrEventManagementForbidden.Err(), status: http.StatusOK},
		{name: "not a member", readErr: eventManagerModel.ErrEventManagementForbidden.Err(), status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &accessContractUC{readErr: tc.readErr, manageErr: tc.manageErr}
			router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var body struct{ Data struct{ CanManage bool } }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.CanManage != tc.canManage {
				t.Fatalf("access response=%s error=%v", w.Body.String(), err)
			}
		})
	}
}

func (u *accessContractUC) ListSolutionAttempts(context.Context, eventUseCase.ListSolutionAttemptsFilter) (eventUseCase.SolutionAttemptsListResult, error) {
	points := int32(100)
	return eventUseCase.SolutionAttemptsListResult{Total: 1, Items: []eventUseCase.SolutionAttemptView{{ID: uuid.Must(uuid.NewV7()), Answer: "flag{sent}", ExpectedFlag: "flag{expected}", Correct: true, Points: &points}}}, nil
}

func TestSolutionAttemptsShowAnswersToManagersOnly(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	url := "/api/events/" + eventID.String() + "/manage/solution-attempts"
	for _, tc := range []struct {
		name        string
		readErr     error
		manageErr   error
		status      int
		withAnswers bool
	}{
		{name: "manager", status: http.StatusOK, withAnswers: true},
		{name: "viewer", manageErr: eventManagerModel.ErrEventManagementForbidden.Err(), status: http.StatusOK},
		{name: "not a member", readErr: eventManagerModel.ErrEventManagementForbidden.Err(), manageErr: eventManagerModel.ErrEventManagementForbidden.Err(), status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &accessContractUC{readErr: tc.readErr, manageErr: tc.manageErr}
			router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var body struct {
				Data struct {
					Items []struct {
						Answer, ExpectedFlag *string
						Points               *int32
					}
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data.Items) != 1 {
				t.Fatalf("response=%s error=%v", w.Body.String(), err)
			}
			item := body.Data.Items[0]
			if (item.Answer != nil) != tc.withAnswers || (item.ExpectedFlag != nil) != tc.withAnswers {
				t.Fatalf("answers visible=%v, want %v: %s", item.Answer != nil, tc.withAnswers, w.Body.String())
			}
			if item.Points == nil || *item.Points != 100 {
				t.Fatalf("points missing: %s", w.Body.String())
			}
		})
	}
}

func (u *accessContractUC) GetManageResults(context.Context, uuid.UUID) (eventUseCase.ManageResultsView, error) {
	return eventUseCase.ManageResultsView{}, nil
}

// routerAs is testEventCRUDRouter with the caller's platform role.
func routerAs(u eventHandler.IUseCase, actor uuid.UUID, role rbac.Role) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler, func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: actor, Role: role}))
		ctx.Next()
	})
	eventHandler.NewEventAPIHandler(u, allowEventCRUD{}).Init(router.Group("api"))
	return router
}

// Exports take data out of the system: the owner, write moderators and platform admins (events.write), as in the
// analytics; a viewer (observer) and an admin viewer read the same data on the screen only (owner rulings 2026-10-19).
func TestExportsAreForManagersAndPlatformAdminsOnly(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	forbidden := eventManagerModel.ErrEventManagementForbidden.Err()
	for _, route := range []string{"results/export.csv", "solution-attempts/export.csv"} {
		for _, tc := range []struct {
			name      string
			role      rbac.Role
			readErr   error
			manageErr error
			status    int
		}{
			{name: "write moderator", role: rbac.RoleUser, status: http.StatusOK},
			{name: "platform admin without a role in the event", role: rbac.RoleAdmin, readErr: forbidden, manageErr: forbidden, status: http.StatusOK},
			{name: "event viewer", role: rbac.RoleUser, manageErr: forbidden, status: http.StatusForbidden},
			{name: "admin viewer", role: rbac.RoleAdminViewer, manageErr: forbidden, status: http.StatusForbidden},
			{name: "not a member", role: rbac.RoleUser, readErr: forbidden, manageErr: forbidden, status: http.StatusForbidden},
		} {
			t.Run(route+" "+tc.name, func(t *testing.T) {
				u := &accessContractUC{readErr: tc.readErr, manageErr: tc.manageErr}
				w := httptest.NewRecorder()
				routerAs(&eventCRUDContractUC{IUseCase: u}, actor, tc.role).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/"+route, nil))
				if w.Code != tc.status {
					t.Fatalf("status=%d, want %d: %s", w.Code, tc.status, w.Body.String())
				}
				if tc.status == http.StatusOK && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
					t.Fatalf("no CSV: %q", w.Header().Get("Content-Type"))
				}
			})
		}
	}
}

// The panel shows the export buttons from CanExport: the same rule as the export routes.
func TestManagementAccessReportsCanExport(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	forbidden := eventManagerModel.ErrEventManagementForbidden.Err()
	for _, tc := range []struct {
		name      string
		role      rbac.Role
		manageErr error
		canExport bool
	}{
		{name: "write moderator", role: rbac.RoleUser, canExport: true},
		{name: "event viewer", role: rbac.RoleUser, manageErr: forbidden},
		{name: "admin viewer", role: rbac.RoleAdminViewer, manageErr: forbidden},
		{name: "platform admin", role: rbac.RoleAdmin, manageErr: forbidden, canExport: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &accessContractUC{manageErr: tc.manageErr}
			w := httptest.NewRecorder()
			routerAs(&eventCRUDContractUC{IUseCase: u}, actor, tc.role).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/access", nil))
			var body struct {
				Data struct{ CanManage, CanExport bool }
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Data.CanExport != tc.canExport {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

type archivedEventUC struct{ *accessContractUC }

func (archivedEventUC) RequireEventWritable(context.Context, uuid.UUID) error {
	return eventModel.ErrEventArchived.Err()
}

// An archived event is read-only, and an export is a read: it still works, while a write answers 409.
func TestArchivedEventStillExportsButRefusesWrites(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	router := routerAs(archivedEventUC{&accessContractUC{}}, actor, rbac.RoleUser)
	for _, route := range []string{"results/export.csv", "solution-attempts/export.csv"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/"+route, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s on an archived event: status=%d %s", route, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/events/"+eventID.String()+"/manage/results/opened", strings.NewReader(`{"Opened":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, request)
	if w.Code != http.StatusConflict {
		t.Fatalf("write on an archived event: status=%d %s", w.Code, w.Body.String())
	}
}

func TestSolutionAttemptsExportRequiresManageAccess(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}
	router := routerAs(&eventCRUDContractUC{IUseCase: u}, actor, rbac.RoleUser)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/solution-attempts/export.csv", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer must not export answers: status=%d body=%s", w.Code, w.Body.String())
	}
}

// An implicit viewer (platform staff with events.read) passes the read gate but
// never the manage gate, so every moderators-team action answers 403.
func TestModeratorsTeamActionsRejectReadOnlyViewer(t *testing.T) {
	actor, eventID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := "/api/events/" + eventID.String() + "/manage/labs/moderators/"
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "challenges"},
		{http.MethodGet, "challenges/" + challengeID.String() + "/lab"},
		{http.MethodPost, "challenges/" + challengeID.String() + "/lab/link"},
		{http.MethodGet, "vpn"},
		{http.MethodGet, "board"},
		{http.MethodGet, "team"},
		{http.MethodPost, "challenges/" + challengeID.String() + "/submit"},
		{http.MethodGet, "challenges/" + challengeID.String() + "/solves"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			u := &accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}
			router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, base+tc.path, nil))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWebsiteSignatureSaveRejectsReadOnlyViewer(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: uc}, actor)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/events/"+eventID.String()+"/manage/appearance", strings.NewReader(`{"FooterSignature":null}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer can change website signature: %d %s", response.Code, response.Body.String())
	}
}
