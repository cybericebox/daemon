package labagent

import (
	"context"
	"errors"
	"testing"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

func TestSweepListingIsLimitedToThisInstanceAndTheOwnedKinds(t *testing.T) {
	a := &fakeAgent{groups: []*labpb.LabGroup{
		{Name: "g-1", CreatedUnixMs: 1700000000123, Status: &labpb.LabGroupStatus{Phase: "Ready"}, Labels: map[string]string{"cybericebox.io/kind": "stand"}},
		{Name: "g-2"},
	}}
	groups, err := newClient(a).ListSweepableGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The selector is what keeps another platform instance's groups out; the tenant is applied by the agent.
	if want := "cybericebox.io/instance=prod,cybericebox.io/kind in (stand,test)"; a.listSelector != want {
		t.Fatalf("selector = %q, want %q", a.listSelector, want)
	}
	if len(groups) != 2 || groups[0].Name != "g-1" || groups[0].Phase != "Ready" || !groups[0].CreatedAt.Equal(time.UnixMilli(1700000000123)) {
		t.Fatalf("groups = %+v", groups)
	}
	if !groups[1].CreatedAt.IsZero() {
		t.Fatalf("an agent that reports no creation time gives a zero time, got %v", groups[1].CreatedAt)
	}
}

func TestFleetSweepSkipsAnUnreadableAgentAndCountsIt(t *testing.T) {
	f := newFleetFixture(t)
	f.a.groups = []*labpb.LabGroup{{Name: "g-a"}}
	f.b.listErr = errors.New("unreachable")
	groups, failed, err := f.fleet.ListSweepableGroups(context.Background())
	if err != nil || failed != 1 || len(groups) != 1 || groups[0].Name != "g-a" || groups[0].Agent != f.am.ID {
		t.Fatalf("groups=%+v failed=%d err=%v", groups, failed, err)
	}
}

func TestFleetDestroysAnOrphanOnTheAgentThatListedItWithoutAPlacementRow(t *testing.T) {
	f := newFleetFixture(t)
	ctx := context.Background()
	if err := f.fleet.DestroyLabGroupOn(ctx, f.bm.ID, "g-orphan"); err != nil {
		t.Fatal(err)
	}
	if f.b.deleted["group"] == nil || f.b.deleted["group"].Items[0].Name != "g-orphan" || len(f.a.deleted) != 0 {
		t.Fatalf("deleted on b=%v on a=%v", f.b.deleted, f.a.deleted)
	}
	if len(f.store.released) != 1 || f.store.released[0] != "g-orphan" {
		t.Fatalf("released = %v", f.store.released)
	}
}
