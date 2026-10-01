package event

import (
	"testing"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

func TestUpdateConfigInputCountdownMerge(t *testing.T) {
	current := eventConfigModel.EventConfig{Countdown: eventConfigModel.CountdownSettings{ShowStart: true, ShowFinish: false, FinishMinutes: 30}}
	on, minutes := true, int32(5)

	kept := UpdateConfigInput{}.toConfigInput(current)
	if kept.Countdown != current.Countdown {
		t.Fatalf("omitted countdown fields must keep %+v, got %+v", current.Countdown, kept.Countdown)
	}
	changed := UpdateConfigInput{ShowFinishCountdown: &on, FinishCountdownMinutes: &minutes}.toConfigInput(current)
	want := eventConfigModel.CountdownSettings{ShowStart: true, ShowFinish: true, FinishMinutes: 5}
	if changed.Countdown != want {
		t.Fatalf("countdown = %+v, want %+v", changed.Countdown, want)
	}
}
