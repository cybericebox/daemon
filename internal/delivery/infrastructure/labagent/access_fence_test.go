package labagent

import (
	"context"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	"github.com/gofrs/uuid"
	"testing"
)

func TestReconcileLabGroupAccessRevisionPreservesExactTargetAndCompleteDeny(t *testing.T) {
	f := &fakeAgent{}
	target := eventLabModel.AccessTarget{Group: "team", ExpectedGroupUID: "group-uid", OperationID: uuid.Must(uuid.NewV7()), Revision: 9007199254740993}
	c := newClient(f)
	if err := c.ReconcileLabGroupAccessRevision(context.Background(), "team", []labAccessModel.ClientPolicy{{Name: "p", AllowedLabs: []string{"shared"}}, {Name: "empty"}}, target); err != nil {
		t.Fatal(err)
	}
	p := f.access.GetPolicies()[0]
	if p.GetOperationId() != target.OperationID.String() || p.GetDesiredRevision() != target.Revision || p.GetExpectedGroupUid() != target.ExpectedGroupUID || len(p.GetRules()) != 1 {
		t.Fatalf("target lost %+v", p)
	}
	target.Group = "other"
	if err := c.ReconcileLabGroupAccessRevision(context.Background(), "team", nil, target); err == nil {
		t.Fatal("foreign target accepted")
	}
}
func TestLabStopWaitsForCurrentAccessFence(t *testing.T) {
	c, a, target := lifecycleFixture()
	wire := releasedWire(target)
	wire.Status.Lifecycle.AccessFenceVpnBootId = ""
	a.labs = append(a.labs, wire)
	got, err := c.ObserveLab(context.Background(), target.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessFenced || got.Allocation.RuntimeState == "Released" {
		t.Fatalf("missing current VPN identity credited %+v", got)
	}
}
