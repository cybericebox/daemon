package eventself

import (
	"encoding/json"
	"testing"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestParticipantSolutionsPageCarriesFreezeState(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		page := eventUseCase.ChallengeSolvesPage{Frozen: frozen}
		body, err := json.Marshal(solvesPageResponse(page))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["Frozen"]) != map[bool]string{false: "false", true: "true"}[frozen] {
			t.Errorf("solutions response Frozen=%s, want %t", fields["Frozen"], frozen)
		}
	}
}
