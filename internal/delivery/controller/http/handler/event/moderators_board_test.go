package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type moderatorsBoardUC struct {
	accessContractUC
	answer   string
	by, next uuid.UUID
	key      uuid.UUID
	cursor   uuid.UUID
	pageSize int
}

func (u *moderatorsBoardUC) ListModeratorsBoard(context.Context, uuid.UUID) ([]eventUseCase.OwnChallengeView, error) {
	return []eventUseCase.OwnChallengeView{{Snapshot: json.RawMessage(`{"name":"Web"}`), BoardPublished: false,
		Prerequisites: []eventUseCase.ChallengePrerequisiteView{}, Files: []eventUseCase.ChallengeFileView{}}}, nil
}

func (u *moderatorsBoardUC) GetModeratorsTeam(context.Context, uuid.UUID) (eventUseCase.ModeratorsTeamView, error) {
	return eventUseCase.ModeratorsTeamView{Members: []eventUseCase.ModeratorsTeamMemberView{{Name: "Олена Коваль", Role: 1}}}, nil
}

func (u *moderatorsBoardUC) SubmitModeratorsChallenge(_ context.Context, _, userID, _ uuid.UUID, in eventUseCase.SubmitChallengeInput) (eventUseCase.SubmitChallengeResult, error) {
	u.answer, u.by, u.key = in.Answer, userID, in.IdempotencyKey
	return eventUseCase.SubmitChallengeResult{Correct: true, FirstSolve: true}, nil
}

func (u *moderatorsBoardUC) ListModeratorsChallengeSolves(_ context.Context, _, _, cursor uuid.UUID, pageSize int) (eventUseCase.ChallengeSolvesPage, error) {
	u.cursor, u.pageSize = cursor, pageSize
	return eventUseCase.ChallengeSolvesPage{Items: []eventUseCase.ChallengeSolveView{{TeamName: "Alpha", Own: true}}, HasMore: true, Next: u.next, Total: 7}, nil
}

func (u *moderatorsBoardUC) SetTeamHidden(_ context.Context, _, teamID uuid.UUID, hidden bool) (eventUseCase.TeamView, error) {
	return eventUseCase.TeamView{ID: teamID, Hidden: hidden}, nil
}

func TestModeratorsBoardRoutes(t *testing.T) {
	actor, eventID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := "/api/events/" + eventID.String() + "/manage/labs/moderators/"
	u := &moderatorsBoardUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"board", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"BoardPublished":false`) || !strings.Contains(w.Body.String(), `"Locked":false`) || !strings.Contains(w.Body.String(), `"SolveCount":null`) {
		t.Fatalf("board: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"team", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Олена Коваль"`) {
		t.Fatalf("team: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, base+"challenges/"+challengeID.String()+"/submit", strings.NewReader(`{"Answer":"ICE{x}"}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Correct":true`) || !strings.Contains(w.Body.String(), `"FirstSolve":true`) || u.answer != "ICE{x}" || u.by != actor || u.key == uuid.Nil {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	u.next = uuid.Must(uuid.NewV7())
	cursor := uuid.Must(uuid.NewV7())
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"challenges/"+challengeID.String()+"/solves?pageSize=25&cursor="+cursor.String(), nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"TeamName":"Alpha"`) || !strings.Contains(w.Body.String(), `"NextCursor":"`+u.next.String()+`"`) ||
		!strings.Contains(w.Body.String(), `"Total":7`) || u.cursor != cursor || u.pageSize != 25 {
		t.Fatalf("solves: %d %s (%s %d)", w.Code, w.Body.String(), u.cursor, u.pageSize)
	}
	teamID := uuid.Must(uuid.NewV7())
	w = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/events/"+eventID.String()+"/manage/teams/"+teamID.String()+"/hidden", strings.NewReader(`{"Hidden":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, request)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Hidden":true`) {
		t.Fatalf("hidden: %d %s", w.Code, w.Body.String())
	}

	observer := &moderatorsBoardUC{accessContractUC: accessContractUC{manageErr: eventManagerModel.ErrEventManagementForbidden.Err()}}
	router = testEventCRUDRouter(&eventCRUDContractUC{IUseCase: observer}, actor)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, base+"board", nil),
		httptest.NewRequest(http.MethodGet, base+"team", nil),
		httptest.NewRequest(http.MethodGet, base+"challenges/"+challengeID.String()+"/solves", nil),
		httptest.NewRequest(http.MethodPut, "/api/events/"+eventID.String()+"/manage/teams/"+uuid.Must(uuid.NewV7()).String()+"/hidden", strings.NewReader(`{"Hidden":true}`)),
		httptest.NewRequest(http.MethodPost, base+"challenges/"+challengeID.String()+"/submit", strings.NewReader(`{"Answer":"x"}`)),
		httptest.NewRequest(http.MethodGet, base+"challenges/"+challengeID.String()+"/files/"+uuid.Must(uuid.NewV7()).String(), nil),
	} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, request)
		if w.Code != http.StatusForbidden {
			t.Fatalf("observer %s %s: %d", request.Method, request.URL.Path, w.Code)
		}
	}
}
