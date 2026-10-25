package labagent

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type failingPlacements struct{ memoryPlacements }

func (*failingPlacements) Get(context.Context, string) (uuid.UUID, bool, error) {
	return uuid.Nil, false, errors.New("database unavailable")
}

// The lifecycle capabilities of a group are tri-state: the owner's report, or
// for an unrecorded group the whole fleet's; a missing report or a failed
// placement read is unknown, never "absent".
func TestLabLifecycleCapabilitiesTriState(t *testing.T) {
	ctx := context.Background()
	full := infraModel.AgentFeatures{Lifecycle: infraModel.LifecycleCapabilities{PerLabStop: true, ConfirmedRuntime: true, FullGroupStop: true}}

	f := newFleetFixture(t)
	if _, known := f.fleet.LabLifecycleCapabilities(ctx, "g"); known {
		t.Fatal("no report yet must be unknown")
	}
	f.am.Features, f.bm.Features = NewFeatureCell(&infraModel.AgentFeatures{}), NewFeatureCell(nil)
	if _, known := f.fleet.LabLifecycleCapabilities(ctx, "g"); known {
		t.Fatal("an unrecorded group with one agent not reported must be unknown")
	}
	f.bm.Features.Set(infraModel.AgentFeatures{})
	if caps, known := f.fleet.LabLifecycleCapabilities(ctx, "g"); !known || caps != (infraModel.LifecycleCapabilities{}) {
		t.Fatalf("an unrecorded group on a fleet without lifecycle: %+v %v", caps, known)
	}
	f.bm.Features.Set(full)
	if caps, known := f.fleet.LabLifecycleCapabilities(ctx, "g"); !known || !caps.FullGroupStop {
		t.Fatalf("an unrecorded group may be on the capable agent: %+v %v", caps, known)
	}
	// A recorded group answers for its owner only.
	f.store.groups = map[string]uuid.UUID{"g": f.am.ID}
	if caps, known := f.fleet.LabLifecycleCapabilities(ctx, "g"); !known || caps.FullGroupStop {
		t.Fatalf("the owner reported no lifecycle: %+v %v", caps, known)
	}
	f.store.groups["g"] = uuid.Must(uuid.NewV7())
	if _, known := f.fleet.LabLifecycleCapabilities(ctx, "g"); known {
		t.Fatal("an owner outside the fleet must be unknown")
	}

	broken := NewFleet(&failingPlacements{}, nil, f.am, f.bm)
	if _, known := broken.LabLifecycleCapabilities(ctx, "g"); known {
		t.Fatal("a failed placement read must be unknown")
	}
}
