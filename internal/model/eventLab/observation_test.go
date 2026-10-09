package eventLabModel

import (
	"github.com/gofrs/uuid"
	"testing"
)

func TestObservationMatchesLiveGenerationAndLifecycleIdentity(t *testing.T) {
	l := Lab{Ref: Ref{"team", "shared"}, AgentUID: "uid", AgentGeneration: 7, OperationID: uuid.FromStringOrNil("00000000-0000-0000-0000-000000000001"), Revision: 4}
	o := Observation{Ref: l.Ref, UID: "uid", Generation: 9, ObservedGeneration: 9, OperationID: l.OperationID, Revision: 4}
	if !ObservationMatches(l, o) {
		t.Fatal("live generation advance refused")
	}
	for name, change := range map[string]func(*Observation){"UID": func(o *Observation) { o.UID = "other" }, "reference": func(o *Observation) { o.Ref.Lab = "other" }, "operation": func(o *Observation) { o.OperationID = uuid.Nil }, "revision": func(o *Observation) { o.Revision = 3 }, "stale status": func(o *Observation) { o.ObservedGeneration = 8 }, "old floor": func(o *Observation) { o.Generation = 6; o.ObservedGeneration = 6 }, "missing generation": func(o *Observation) { o.Generation = 0; o.ObservedGeneration = 0 }} {
		t.Run(name, func(t *testing.T) {
			next := o
			change(&next)
			if ObservationMatches(l, next) {
				t.Fatal("matched stale identity")
			}
		})
	}
}
