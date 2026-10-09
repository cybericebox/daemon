package event

import (
	"encoding/json"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"strings"
	"testing"
	"time"
)

func TestParticipantLabViewClosedSettlesBeforePhysicalStop(t *testing.T) {
	at := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	for _, state := range []string{"Snapshotting", "Stopping", "StopFailed"} {
		out := participantLabView(eventLabModel.Lab{Revision: 9007199254740993, ActualState: state, ClosedAt: &at, CloseReason: "solved", SnapshotMode: "required", RetentionUntil: &at})
		if !out.LogicalClosed || out.RuntimeState != "closed" || out.CanStop || out.CanRestart || out.SnapshotPolicy != "required" || out.RetentionUntil == nil {
			t.Fatal(out)
		}
		body, err := json.Marshal(out)
		if err != nil || !strings.Contains(string(body), `"Revision":"9007199254740993"`) {
			t.Fatal(string(body), err)
		}
	}
}
