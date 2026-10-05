package eventConfigModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

func TestNewEventConfigCountdownDefaults(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	want := eventConfigModel.CountdownSettings{ShowStart: true, ShowFinish: true, FinishMinutes: 10, FinishMode: eventConfigModel.FinishBeforeEnd}
	if c.Countdown != want {
		t.Fatalf("countdown defaults = %+v, want %+v", c.Countdown, want)
	}
}

func TestUpdateCountdown(t *testing.T) {
	later := cfgNow.Add(time.Hour)
	actor := uuid.Must(uuid.NewV7())
	newConfig := func() eventConfigModel.EventConfig {
		return eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	}

	t.Run("applies", func(t *testing.T) {
		c := newConfig()
		in := newValidInput()
		in.Countdown = eventConfigModel.CountdownSettings{ShowStart: false, ShowFinish: true, FinishMinutes: 25}
		if err := c.Update(in, later, actor); err != nil {
			t.Fatalf("update: %v", err)
		}
		if c.Countdown != in.Countdown {
			t.Fatalf("countdown = %+v, want %+v", c.Countdown, in.Countdown)
		}
	})
	t.Run("omitted keeps the current value", func(t *testing.T) {
		c := newConfig()
		c.Countdown = eventConfigModel.CountdownSettings{ShowStart: false, ShowFinish: false, FinishMinutes: 30}
		if err := c.Update(newValidInput(), later, actor); err != nil {
			t.Fatalf("update: %v", err)
		}
		if c.Countdown.FinishMinutes != 30 || c.Countdown.ShowStart || c.Countdown.ShowFinish {
			t.Fatalf("omitted countdown must be kept, got %+v", c.Countdown)
		}
	})
	t.Run("rejects minutes out of range", func(t *testing.T) {
		for _, minutes := range []int32{-1, 1441} {
			c := newConfig()
			in := newValidInput()
			in.Countdown = eventConfigModel.CountdownSettings{ShowStart: true, ShowFinish: true, FinishMinutes: minutes}
			if err := c.Update(in, later, actor); err == nil {
				t.Fatalf("minutes %d must be rejected", minutes)
			}
		}
	})
	t.Run("accepts the bounds", func(t *testing.T) {
		for _, minutes := range []int32{1, 1440} {
			c := newConfig()
			in := newValidInput()
			in.Countdown = eventConfigModel.CountdownSettings{ShowFinish: true, FinishMinutes: minutes}
			if err := c.Update(in, later, actor); err != nil {
				t.Fatalf("minutes %d: %v", minutes, err)
			}
		}
	})
}
