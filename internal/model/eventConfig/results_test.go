package eventConfigModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

func TestResultsSettingsDefaults(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	want := eventConfigModel.ResultsSettings{FreezeMinutes: 60, LiveFreeze: true, ChartEnabled: true, ChartTeams: 10}
	if c.Results.FreezeEnabled || c.Results.FreezeMinutes != want.FreezeMinutes || !c.Results.LiveFreeze || !c.Results.ChartEnabled ||
		c.Results.ChartTeams != want.ChartTeams || c.Results.RowsLimit != nil || c.Results.OpenedAt != nil {
		t.Fatalf("results defaults: %+v", c.Results)
	}
}

func TestFreezeWindow(t *testing.T) {
	finish := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)
	s := eventConfigModel.ResultsSettings{FreezeEnabled: true, FreezeMinutes: 30}
	if at := s.FrozenAt(&finish); at == nil || !at.Equal(finish.Add(-30*time.Minute)) {
		t.Fatalf("FrozenAt = %v", at)
	}
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before", finish.Add(-31 * time.Minute), false},
		{"at freeze", finish.Add(-30 * time.Minute), true},
		{"inside", finish.Add(-time.Minute), true},
		{"at finish", finish, false},
		{"after", finish.Add(time.Hour), false},
	}
	for _, tc := range cases {
		if got := s.FreezeActive(&finish, tc.now); got != tc.want {
			t.Fatalf("%s: FreezeActive = %v, want %v", tc.name, got, tc.want)
		}
	}
	if s.FreezeActive(nil, finish.Add(-time.Minute)) || (eventConfigModel.ResultsSettings{FreezeMinutes: 30}).FreezeActive(&finish, finish.Add(-time.Minute)) {
		t.Fatal("no finish or a disabled freeze must never freeze")
	}
	opened := finish.Add(-10 * time.Minute)
	s.OpenedAt = &opened
	if s.FreezeActive(&finish, finish.Add(-5*time.Minute)) {
		t.Fatal("opened results must not stay frozen")
	}
}

func TestSetResultsSettings(t *testing.T) {
	by := uuid.Must(uuid.NewV7())
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	opened := cfgNow.Add(-time.Hour)
	c.Results.OpenedAt = &opened
	rows := int32(20)
	later := cfgNow.Add(time.Minute)
	in := eventConfigModel.ResultsSettings{FreezeEnabled: true, FreezeMinutes: 30, ChartEnabled: false, ChartTeams: 5, RowsLimit: &rows}
	if err := c.SetResultsSettings(eventConfigModel.VisibilityPublic, in, later, by); err != nil {
		t.Fatalf("SetResultsSettings: %v", err)
	}
	if c.ScoreboardVisibility != eventConfigModel.VisibilityPublic || !c.Results.FreezeEnabled || c.Results.FreezeMinutes != 30 ||
		c.Results.ChartTeams != 5 || *c.Results.RowsLimit != 20 || c.Results.LiveFreeze {
		t.Fatalf("settings not applied: %+v", c.Results)
	}
	if c.Results.OpenedAt == nil || !c.Results.OpenedAt.Equal(opened) {
		t.Fatal("saving settings must keep the early opening")
	}
	if !c.UpdatedAt.Equal(later) || c.UpdatedBy.UUID != by {
		t.Fatal("settings must touch the config")
	}
	zero, big := int32(0), int32(1001)
	for _, bad := range []eventConfigModel.ResultsSettings{
		{FreezeMinutes: 0, ChartTeams: 5}, {FreezeMinutes: 1441, ChartTeams: 5}, {FreezeMinutes: 30, ChartTeams: 0},
		{FreezeMinutes: 30, ChartTeams: 11}, {FreezeMinutes: 30, ChartTeams: 5, RowsLimit: &zero}, {FreezeMinutes: 30, ChartTeams: 5, RowsLimit: &big},
	} {
		if err := c.SetResultsSettings(eventConfigModel.VisibilityPublic, bad, later, by); !errors.Is(err, eventConfigModel.ErrResultsSettingsInvalid.Err()) {
			t.Fatalf("%+v: got %v", bad, err)
		}
	}
	if err := c.SetResultsSettings(eventConfigModel.Visibility(9), in, later, by); !errors.Is(err, eventConfigModel.ErrVisibilityInvalid.Err()) {
		t.Fatalf("invalid visibility: %v", err)
	}
}

func TestSetResultsOpened(t *testing.T) {
	by := uuid.Must(uuid.NewV7())
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	first := cfgNow.Add(time.Minute)
	c.SetResultsOpened(true, first, by)
	c.SetResultsOpened(true, first.Add(time.Minute), by)
	if c.Results.OpenedAt == nil || !c.Results.OpenedAt.Equal(first) {
		t.Fatalf("opening twice must keep the first moment: %v", c.Results.OpenedAt)
	}
	c.SetResultsOpened(false, first.Add(2*time.Minute), by)
	if c.Results.OpenedAt != nil {
		t.Fatal("closing must restore the freeze")
	}
}
