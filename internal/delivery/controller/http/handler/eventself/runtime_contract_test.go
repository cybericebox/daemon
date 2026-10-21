package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
)

type runtimeContractUseCase struct {
	fakeUseCase
	status                       exerciseModel.LabDeployStatus
	eventID, userID, challengeID uuid.UUID
}

func (f runtimeContractUseCase) GetOwnChallengeRuntime(_ context.Context, eventID, userID, challengeID uuid.UUID) (eventUseCase.ChallengeRuntimeView, error) {
	if eventID != f.eventID || userID != f.userID || challengeID != f.challengeID {
		panic("participant runtime requested with wrong identity")
	}
	return eventUseCase.ChallengeRuntimeView{Status: f.status}, nil
}

func TestParticipantRuntimeOmitsSchedulerDiagnostics(t *testing.T) {
	eventID, userID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	at := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	for _, state := range []struct {
		phase string
		ready bool
	}{{"Queued", false}, {"Running", true}} {
		t.Run(state.phase, func(t *testing.T) {
			status := exerciseModel.LabDeployStatus{Phase: state.phase, Ready: state.ready,
				VPNCIDR: "10.8.0.0/24", InternetCIDR: "10.9.0.0/24",
				Access: []exerciseModel.LabAccess{{Device: "web", Port: 80, Protocol: "http", URL: "http://10.8.0.2"}},
				Queue:  &exerciseModel.LabQueue{Position: 2, Length: 8, Reason: "NodeStarting", Message: "private scheduler detail", Pods: 4, Pending: 2, Attempt: 3, RetryAt: at, WaitingSince: at},
			}
			h := NewEventSelfAPIHandler(runtimeContractUseCase{status: status, eventID: eventID, userID: userID, challengeID: challengeID}, allowAllProtection{})
			w := httptest.NewRecorder()
			participantPagesRouter(h, userID, eventID).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/challenges/"+challengeID.String()+"/lab", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("runtime status=%d body=%s", w.Code, w.Body.String())
			}
			var outer struct{ Data map[string]json.RawMessage }
			if err := json.Unmarshal(w.Body.Bytes(), &outer); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"Queue", "Message", "Reason", "Position", "Length", "Pods", "Pending", "Attempt", "RetryAt", "WaitingSince"} {
				if _, exists := outer.Data[field]; exists {
					t.Fatalf("participant runtime exposes %s: %s", field, w.Body.String())
				}
			}
			var phase string
			var ready bool
			if err := json.Unmarshal(outer.Data["Phase"], &phase); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(outer.Data["Ready"], &ready); err != nil {
				t.Fatal(err)
			}
			if phase != state.phase || ready != state.ready || string(outer.Data["Access"]) != `[{"Device":"web","Port":80,"Protocol":"http","URL":"http://10.8.0.2"}]` {
				t.Fatalf("generic runtime status or task access lost: %s", w.Body.String())
			}
		})
	}
}
