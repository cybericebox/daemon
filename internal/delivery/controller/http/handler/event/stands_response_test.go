package event

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestStandsResponseCarriesTheQueueAndImageWarningPerStand(t *testing.T) {
	queued := eventUseCase.StandTeamView{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Red", Status: eventStandModel.StatusCreating,
		Launch: labMonitoringModel.Launch{QueuedLabs: 3, Position: 2, Length: 9, Reason: "WaitingForGroup", ImageWarning: true}}
	calm := eventUseCase.StandTeamView{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Blue", Status: eventStandModel.StatusReady}
	raw, err := json.Marshal(toStandsResponse(eventUseCase.StandsView{Items: []eventUseCase.StandTeamView{queued, calm}}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{`"Queue":{"QueuedLabs":3,"Position":2,"Length":9,"Reason":"WaitingForGroup"}`, `"ImageWarning":true`, `"Queue":null,"ImageWarning":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("stands response lacks %s: %s", want, body)
		}
	}
}
