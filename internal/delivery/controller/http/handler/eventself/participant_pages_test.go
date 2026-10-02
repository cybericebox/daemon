package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestOwnChallengeResponseCarriesBoardContract(t *testing.T) {
	count := int64(3)
	prerequisite, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	body, err := json.Marshal(toOwnChallengeResponse(eventUseCase.OwnChallengeView{
		Snapshot: json.RawMessage(`{"name":"x"}`), Infrastructure: true, HintsEnabled: true, SolveCount: &count,
		Prerequisites: []eventUseCase.ChallengePrerequisiteView{{EventChallengeID: prerequisite, Name: "Intro", Solved: true}},
		Files:         []eventUseCase.ChallengeFileView{{FileID: fileID, Name: "a.zip", Size: 12345}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"ContentUpdatedAt":null`, `"Infrastructure":true`, `"HintsEnabled":true`, `"Locked":false`,
		`"Prerequisites":[{"EventChallengeID":"` + prerequisite.String() + `","Name":"Intro","Solved":true}]`,
		`"Files":[{"FileID":"` + fileID.String() + `","Name":"a.zip","Size":12345}]`, `"SolveCount":3`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("missing %s in %s", expected, body)
		}
	}
	empty, _ := json.Marshal(toOwnChallengeResponse(eventUseCase.OwnChallengeView{}))
	for _, expected := range []string{`"Prerequisites":[]`, `"Files":[]`, `"SolveCount":null`} {
		if !strings.Contains(string(empty), expected) {
			t.Fatalf("missing %s in %s", expected, empty)
		}
	}
}

func TestOwnHintResponseHidesLevel(t *testing.T) {
	body, err := json.Marshal(ToOwnHintResponses([]eventUseCase.OwnHintView{{ID: uuid.Must(uuid.NewV7()), Level: "near_solution", Cost: 40}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Level") || strings.Contains(string(body), "near_solution") || !strings.Contains(string(body), `"Cost":40`) {
		t.Fatalf("participant hint leaks the level or lost the price: %s", body)
	}
}

func participantPagesRouter(h *Handler, userID, eventID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler, func(ctx *gin.Context) {
		c := rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: userID})
		ctx.Request = ctx.Request.WithContext(middleware.ContextWithEventTenant(c, middleware.EventTenant{EventID: eventID}))
	})
	h.Init(router.Group("/api"), func(*gin.Context) {})
	return router
}

func TestChallengeSolvesAndTeamMembersRoutes(t *testing.T) {
	eventID, userID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	solvedAt := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	h := NewEventSelfAPIHandler(fakeUseCase{
		solves: func(_ context.Context, gotEvent, gotUser, gotChallenge uuid.UUID) ([]eventUseCase.ChallengeSolveView, error) {
			if gotEvent != eventID || gotUser != userID || gotChallenge != challengeID {
				t.Fatalf("solves identity: %s %s %s", gotEvent, gotUser, gotChallenge)
			}
			return []eventUseCase.ChallengeSolveView{{TeamName: "Red", SolvedAt: solvedAt, Own: true, FirstBlood: true}}, nil
		},
		members: func(context.Context, uuid.UUID, uuid.UUID) ([]eventUseCase.TeamRosterMemberView, error) {
			return []eventUseCase.TeamRosterMemberView{{UserID: userID, DisplayName: "Neo", Role: participantModel.TeamRoleCaptain, Own: true}}, nil
		},
	}, allowAllProtection{})
	router := participantPagesRouter(h, userID, eventID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/challenges/"+challengeID.String()+"/solves", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `{"TeamName":"Red","NameHidden":false,"SolvedAt":"2026-09-29T10:00:00Z","Own":true,"FirstBlood":true}`) || !strings.Contains(w.Body.String(), `"Total":1`) {
		t.Fatalf("solves: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/mine/members", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `{"UserID":"`+userID.String()+`","DisplayName":"Neo","Role":0,"Own":true,"Pending":false}`) {
		t.Fatalf("members: %d %s", w.Code, w.Body.String())
	}
}

func TestParticipantAnswersRoutes(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var submitted map[string]any
	h := NewEventSelfAPIHandler(fakeUseCase{
		answers: func(_ context.Context, _, _ uuid.UUID, answers map[string]any) (eventUseCase.OwnParticipantAnswersView, error) {
			submitted = answers
			if answers != nil && answers["school"] != nil {
				return eventUseCase.OwnParticipantAnswersView{}, participantModel.ErrParticipantFieldNotEditable.Err()
			}
			return eventUseCase.OwnParticipantAnswersView{Form: eventUseCase.ParticipantFormView{Version: 2, Enabled: true}, Answers: map[string]any{"city": "Lviv"}, Editable: true}, nil
		},
	}, allowAllProtection{})
	router := participantPagesRouter(h, userID, eventID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/self/participant-answers", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Answers":{"city":"Lviv"}`) || !strings.Contains(w.Body.String(), `"Editable":true`) ||
		!strings.Contains(w.Body.String(), `"Form":{"Version":2,"Enabled":true,"Required":false`) {
		t.Fatalf("get answers: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/events/self/participant-answers", strings.NewReader(`{"Answers":{"city":"Lviv"}}`)))
	if w.Code != http.StatusOK || submitted["city"] != "Lviv" {
		t.Fatalf("put answers: %d %s %v", w.Code, w.Body.String(), submitted)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/events/self/participant-answers", strings.NewReader(`{"Answers":{"school":"LNU"}}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-editable answer: %d %s", w.Code, w.Body.String())
	}
}
