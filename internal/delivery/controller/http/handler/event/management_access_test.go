package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
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

func TestSolutionAttemptsExportRequiresManageAccess(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
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
