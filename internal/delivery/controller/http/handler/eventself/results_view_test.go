package eventself

import (
	"encoding/json"
	"strings"
	"testing"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestResultsViewAcceptsOnlyPageAndLive(t *testing.T) {
	if live, err := resultsView(""); err != nil || live {
		t.Fatalf("empty view = %v, %v", live, err)
	}
	if live, err := resultsView("live"); err != nil || !live {
		t.Fatalf("live view = %v, %v", live, err)
	}
	if _, err := resultsView("admin"); err == nil {
		t.Fatal("unknown view must be rejected")
	}
}

// The live screen facts (Tasks, MaxPoints) ride along only when set: the
// results page leaves both out of its JSON.
func TestResultsSnapshotResponseLiveFacts(t *testing.T) {
	tasks, maxPoints := int64(12), int64(1450)
	live, err := json.Marshal(toResultsSnapshotResponse(eventUseCase.ResultsSnapshotView{Tasks: &tasks, MaxPoints: &maxPoints}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), `"Tasks":12`) || !strings.Contains(string(live), `"MaxPoints":1450`) {
		t.Fatalf("live snapshot = %s", live)
	}
	page, err := json.Marshal(toResultsSnapshotResponse(eventUseCase.ResultsSnapshotView{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), `"Tasks"`) || strings.Contains(string(page), `"MaxPoints"`) {
		t.Fatalf("page snapshot = %s", page)
	}
}
