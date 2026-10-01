package event

import (
	"encoding/json"
	"strings"
	"testing"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestConfigCarriesTheTaskRevealMode(t *testing.T) {
	raw, err := json.Marshal(toConfigResponse(eventUseCase.EventConfigView{TaskRevealMode: eventConfigModel.RevealAsReady}))
	if err != nil || !strings.Contains(string(raw), `"TaskRevealMode":"as_ready"`) {
		t.Fatalf("response = %s, %v", raw, err)
	}
	var omitted updateConfigRequest
	if got := omitted.toInput().TaskRevealMode; got != nil {
		t.Fatalf("an omitted mode keeps the current one, got %v", *got)
	}
	var set updateConfigRequest
	if err = json.Unmarshal([]byte(`{"TaskRevealMode":"all_ready"}`), &set); err != nil {
		t.Fatal(err)
	}
	if got := set.toInput().TaskRevealMode; got == nil || *got != eventConfigModel.RevealAllReady {
		t.Fatalf("mode = %v", got)
	}
	var unknown updateConfigRequest
	_ = json.Unmarshal([]byte(`{"TaskRevealMode":"later"}`), &unknown)
	if got := unknown.toInput().TaskRevealMode; got == nil || got.Valid() {
		t.Fatal("an unknown name must reach the domain as an invalid mode")
	}
}
