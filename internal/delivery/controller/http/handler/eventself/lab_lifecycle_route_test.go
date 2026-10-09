package eventself

import (
	"context"
	"encoding/json"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type lifecycleRouteUseCase struct {
	fakeUseCase
	lab                          eventUseCase.ParticipantLabView
	eventID, userID, challengeID uuid.UUID
}

func (f *lifecycleRouteUseCase) GetOwnChallengeRuntime(_ context.Context, e, u, q uuid.UUID) (eventUseCase.ChallengeRuntimeView, error) {
	f.eventID = e
	f.userID = u
	f.challengeID = q
	return eventUseCase.ChallengeRuntimeView{Lab: &f.lab, Status: exerciseModel.LabDeployStatus{Phase: "Closed", Access: []exerciseModel.LabAccess{}}}, nil
}
func (f *lifecycleRouteUseCase) SubmitChallenge(_ context.Context, e, u, q uuid.UUID, _ eventUseCase.SubmitChallengeInput) (eventUseCase.SubmitChallengeResult, error) {
	f.eventID = e
	f.userID = u
	f.challengeID = q
	return eventUseCase.SubmitChallengeResult{Correct: true, FirstSolve: true, Lab: &f.lab}, nil
}
func (f *lifecycleRouteUseCase) ListOwnBoard(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.OwnBoardView, error) {
	return eventUseCase.OwnBoardView{Challenges: []eventUseCase.OwnChallengeView{{EventExerciseID: f.lab.EventExerciseID, Lab: &f.lab}}}, nil
}
func TestBoardRuntimeSubmissionShareFrozenCanonicalLabWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	reason := "solved"
	lab := eventUseCase.ParticipantLabView{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), Revision: "9007199254740993", LogicalClosed: true, CloseReason: &reason, ClosedAt: &at, RuntimeState: "closed", SnapshotPolicy: "required"}
	fake := &lifecycleRouteUseCase{lab: lab}
	h := NewEventSelfAPIHandler(fake, nil)
	eventID, userID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var expected string
	for _, kind := range []string{"board", "runtime", "submission"} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Answer":"flag"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.Must(uuid.NewV7()).String())
		ctx.Request = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{UserID: userID}))
		ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "challengeID", Value: challengeID.String()}}
		switch kind {
		case "board":
			h.listOwnChallenges(ctx)
		case "runtime":
			h.labStatus(ctx)
		case "submission":
			h.submitChallenge(ctx)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", kind, w.Code, w.Body.String())
		}
		var outer struct{ Data json.RawMessage }
		if err := json.Unmarshal(w.Body.Bytes(), &outer); err != nil {
			t.Fatal(err)
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(outer.Data, &data); err != nil {
			t.Fatal(err)
		}
		raw := data["Lab"]
		if kind == "board" {
			var questions []map[string]json.RawMessage
			if err := json.Unmarshal(data["Challenges"], &questions); err != nil {
				t.Fatal(err)
			}
			raw = questions[0]["Lab"]
		} else {
			if fake.eventID != eventID || fake.userID != userID || fake.challengeID != challengeID {
				t.Fatal("authenticated identity lost")
			}
		}
		if expected == "" {
			expected = string(raw)
		} else if string(raw) != expected {
			t.Fatalf("%s Lab disagrees %s vs %s", kind, raw, expected)
		}
		if kind == "runtime" && (string(data["Ready"]) != "false" || string(data["Access"]) != "[]") {
			t.Fatalf("closed access leaks %s", outer.Data)
		}
	}
}
