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
	for _, want := range []string{`"Queue":{"QueuedLabs":3,"Position":2,"Length":9,"Reason":"WaitingForGroup","NodeStartingLabs":0,"Attempt":0,"RetryAt":null,"WaitingSince":null}`, `"ImageWarning":true`, `"Queue":null,"ImageWarning":false`} {
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

// A lab waiting for room carries its wait code and the need and the room, for the organizers' screen.
func TestStandsResponseCarriesTheNeedAndTheRoom(t *testing.T) {
	team := eventUseCase.StandTeamView{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Red", Labs: []eventUseCase.StandLabView{
		{ChallengeName: "Web", WaitReason: eventStandModel.WaitNotEnoughReserved, Wait: &eventStandModel.WaitDetail{NeedCPUMillicores: 1848, NeedMemoryBytes: 7 << 30, HaveCPUMillicores: 1352, HaveMemoryBytes: 5 << 30}},
		{ChallengeName: "Db"},
	}}
	raw, err := json.Marshal(toStandsResponse(eventUseCase.StandsView{Items: []eventUseCase.StandTeamView{team}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"WaitReason":"not_enough_reserved"`, `"Wait":{"NeedCPUMillicores":1848,"NeedMemoryBytes":7516192768,"HaveCPUMillicores":1352,"HaveMemoryBytes":5368709120,"NeedStorageBytes":0,"HaveStorageBytes":0}`, `"Wait":null`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("response lacks %s: %s", want, raw)
		}
	}
}
