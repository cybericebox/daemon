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

// A stand refused for an unavailable image shows the stored reason unchanged, on the stand and its lab.
func TestStandsResponseKeepsTheImageUnavailableReason(t *testing.T) {
	reason := eventStandModel.ImageUnavailableReason("platform:web@sha256:" + strings.Repeat("a", 64))
	failed := eventUseCase.StandTeamView{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Red", Status: eventStandModel.StatusFailed, Reason: reason,
		Labs: []eventUseCase.StandLabView{{ChallengeName: "Web", Reason: reason}}}
	out := toStandsResponse(eventUseCase.StandsView{Items: []eventUseCase.StandTeamView{failed}})
	if got := out.Items[0]; got.Reason != reason || got.Status != "failed" || got.Labs[0].Reason != reason {
		t.Fatalf("stand response %+v", got)
	}
}

// The prewarm summary counts the images skipped as unavailable on their own.
func TestStandsResponseCountsUnavailablePrewarmImages(t *testing.T) {
	raw, err := json.Marshal(toStandsResponse(eventUseCase.StandsView{Prewarm: &eventUseCase.PrewarmSummary{Total: 3, Warming: 2, Unavailable: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"Unavailable":1`) {
		t.Fatalf("prewarm response %s", raw)
	}
}
