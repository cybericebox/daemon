package event_test

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"testing"
	"time"
)

func TestACLMonitoringMapsIdleTeamWithoutQuestionBindings(t *testing.T) {
	f := newStandFixture(t)
	group := testLabGroup(f.eventID, f.blueID)
	ctx := context.Background()
	now := time.Now()
	// The team's initial VPN boot exists before any Lab/question is attached.
	payload := json.RawMessage(`{"groups":[{"name":"` + group + `","uid":"group-uid"}]}`)
	if err := eventLabObservationRepo.New(f.db.Queries).ApplyCurrent(ctx, group, "agent", 1, now, now, true, payload); err != nil {
		t.Fatal(err)
	}
	row, err := f.db.Queries.GetLabMonitoringCurrent(ctx, postgres.GetLabMonitoringCurrentParams{EventID: f.eventID, EventTeamID: f.blueID, LabGroupName: group})
	if err != nil || len(row.Payload) == 0 {
		t.Fatalf("idle group dropped: %s %v", row.Payload, err)
	}
}
