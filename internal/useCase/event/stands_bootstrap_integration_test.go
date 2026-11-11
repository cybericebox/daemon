package event_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// A variant that asks for what the laboratory cannot apply yet (a bootstrap script here) never reaches the agent: the
// stands fail with the reason instead of starting half-working.
func TestStandEngine_RefusesAVariantTheLaboratoryCannotApplyYet(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	var versionID uuid.UUID
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT ee.exercise_version_id FROM event_challenges c
		JOIN event_exercises ee ON ee.id = c.event_exercise_id WHERE c.id = $1`, f.infraOne).
		Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	variant := standVariant(f.infraTaskID)
	variant.Topology.Devices = []exerciseModel.Device{
		{ID: f.infraDevice, Name: "web", Type: exerciseModel.DeviceTypeNode,
			Bootstrap: exerciseModel.Bootstrap{Script: "echo hi"}},
	}
	f.sets.variants[versionID] = variant
	f.pass(t)
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("nothing may be deployed: %v", f.agent.deployed)
	}
	f.assertStatuses(t, map[string]string{"Blue": "failed", "Red": "failed"})
	view, err := f.uc.GetEventStands(context.Background(), f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Items {
		if !strings.Contains(item.Reason, "device web: bootstrap.script") {
			t.Fatalf("stand %s reason %q must name what is not applied", item.TeamName, item.Reason)
		}
	}
}
