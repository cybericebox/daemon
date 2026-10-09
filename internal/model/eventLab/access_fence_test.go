package eventLabModel

import (
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

func TestAccessFenceRequiresCurrentExactPhysicalAcknowledgement(t *testing.T) {
	target := AccessTarget{Group: "group", ExpectedGroupUID: "group-uid", OperationID: uuid.Must(uuid.NewV7()), Revision: 42}
	good := AccessFenceObservation{Group: target.Group, ExpectedGroupUID: target.ExpectedGroupUID, PolicyUID: "policy-uid", OperationID: target.OperationID, DesiredRevision: 42, AppliedRevision: 42, Generation: 3, ObservedGeneration: 3, State: "Applied", VPNBootID: "current", ObservedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	if !AccessFenceMatches(target, good, "current") {
		t.Fatal("valid acknowledgement rejected")
	}
	cases := []AccessFenceObservation{good, good, good, good, good, good, good, good}
	cases[0].State = "Accepted"
	cases[1].AppliedRevision = 41
	cases[2].ObservedGeneration = 2
	cases[3].PolicyUID = ""
	cases[4].ExpectedGroupUID = "old"
	cases[5].OperationID = uuid.Must(uuid.NewV7())
	cases[6].VPNBootID = "old"
	cases[7].DesiredRevision = 41
	for i, got := range cases {
		if AccessFenceMatches(target, got, "current") {
			t.Fatalf("stale acknowledgement %d accepted", i)
		}
	}
	if AccessFenceMatches(target, good, "") {
		t.Fatal("unknown current VPN boot accepted")
	}
}
