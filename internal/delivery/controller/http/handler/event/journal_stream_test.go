package event_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// journalStreamUC returns the first stamp once, then the second one.
type journalStreamUC struct {
	liveContractUC
	mu     sync.Mutex
	stamps []eventUseCase.SolutionAttemptsStampView
}

func (u *journalStreamUC) GetSolutionAttemptsStamp(context.Context, uuid.UUID) (eventUseCase.SolutionAttemptsStampView, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	stamp := u.stamps[0]
	if len(u.stamps) > 1 {
		u.stamps = u.stamps[1:]
	}
	return stamp, nil
}

func TestLiveJournalStreamSplitsAttemptAndHintChanges(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &journalStreamUC{stamps: []eventUseCase.SolutionAttemptsStampView{
		{Attempts: 3, Decisions: 1, HintUnlocks: 2},
		{Attempts: 3, Decisions: 1, HintUnlocks: 3},
	}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/solution-attempts/live?pollInterval=2", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d headers=%v", recorder.Code, recorder.Header())
	}
	// Initial state of both, then only the hints changed.
	if got := strings.Count(body, "event: attempts-changed\n"); got != 1 {
		t.Fatalf("attempts-changed events = %d, body:\n%s", got, body)
	}
	if !strings.Contains(body, "event: hints-changed\ndata: {\"HintUnlocks\":2}\n\n") || !strings.Contains(body, "event: hints-changed\ndata: {\"HintUnlocks\":3}\n\n") {
		t.Fatalf("hints-changed events missing, body:\n%s", body)
	}
}
