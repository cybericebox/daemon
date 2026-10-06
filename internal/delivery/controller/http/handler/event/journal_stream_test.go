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

	"github.com/cybericebox/daemon/internal/delivery/controller/http/sse"
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

// L18: one account cannot hold an unbounded number of journal streams open.
func TestLiveJournalStreamsArePerUserLimited(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var releases []func()
	for i := 0; i < 6; i++ {
		release, ok := sse.Streams.Acquire("attempts:"+actor.String(), 6)
		if !ok {
			t.Fatalf("slot %d", i)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	u := &journalStreamUC{stamps: []eventUseCase.SolutionAttemptsStampView{{Attempts: 1}}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/manage/solution-attempts/live", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("the 7th stream of one account must be refused with 429, got %d", recorder.Code)
	}
}
