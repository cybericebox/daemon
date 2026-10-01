package eventself

import (
	"encoding/json"
	"strings"
	"testing"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// The join info carries the computed participation block, so the event site
// renders capabilities and reason codes instead of deriving the rules itself.
func TestJoinInfoResponseCarriesParticipationBlock(t *testing.T) {
	state := eventUseCase.ParticipationState{
		Phase: eventModel.LifecycleStarted, Staff: true,
		Register:           eventUseCase.Capability{Reason: eventUseCase.ReasonStaff},
		RegistrationReason: eventUseCase.ReasonClosedAtStart, RosterReason: eventUseCase.ReasonRosterFrozen,
	}
	raw, err := json.Marshal(toJoinInfoResponse(eventUseCase.JoinInfoView{Participation: &state}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Phase":"started"`, `"Staff":true`, `"Register":{"Allowed":false,"Reason":"staff_cannot_participate"}`, `"RegistrationReason":"closed_at_start"`, `"RosterReason":"roster_frozen_at_start"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
	plain, _ := json.Marshal(toJoinInfoResponse(eventUseCase.JoinInfoView{}))
	if strings.Contains(string(plain), "Participation") {
		t.Fatalf("a join result carries no participation block: %s", plain)
	}
}
