// internal/model/eventConfig/config_test.go
package eventConfigModel_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

func TestConfigDoesNotRetainLegacyRegistrationWindow(t *testing.T) {
	configType := reflect.TypeOf(eventConfigModel.EventConfig{})
	if _, exists := configType.FieldByName("RegistrationAfterStart"); exists {
		t.Fatal("registration window must be represented only by event JoinPolicy")
	}
}

var cfgNow = time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)

func newValidInput() eventConfigModel.ConfigInput {
	return eventConfigModel.ConfigInput{
		Registration:           eventConfigModel.RegistrationApproval,
		ScoreboardVisibility:   eventConfigModel.VisibilityPublic,
		ParticipantsVisibility: eventConfigModel.VisibilityPrivate,
		PreviewDescription:     "A friendly CTF",
		PreviewPicture:         "https://cdn.example.test/x.png",
		MaxTeamSize:            5,
	}
}

func TestNewEventConfigDefaults(t *testing.T) {
	eid := uuid.Must(uuid.NewV7())
	c := eventConfigModel.NewEventConfig(eid, cfgNow)
	if c.EventID != eid {
		t.Fatalf("EventID: got %v", c.EventID)
	}
	if c.Participation != nil {
		t.Fatalf("Participation must default nil (unset)")
	}
	if c.Registration != eventConfigModel.RegistrationClose {
		t.Fatalf("Registration default must be Close, got %d", c.Registration)
	}
	if c.ScoreboardVisibility != eventConfigModel.VisibilityHidden || c.ParticipantsVisibility != eventConfigModel.VisibilityHidden {
		t.Fatalf("visibility defaults must be Hidden")
	}
	if c.MaxTeamSize != 5 || c.MinTeamSize != nil || c.MaxTeams != nil {
		t.Fatalf("team limit defaults must be safe: %+v", c)
	}
	if c.Theme.Brand != "#211A52" || c.Theme.Accent != "" || c.Theme.AccentLight != "#211A52" || c.Theme.AccentDark != "#E6E6EE" || c.Theme.AccentLive != "#FFFFFF" || c.Theme.Version != 1 {
		t.Fatalf("theme defaults must match the platform palette: %+v", c.Theme)
	}
	if !c.CreatedAt.Equal(cfgNow) || !c.UpdatedAt.Equal(cfgNow) {
		t.Fatalf("timestamps must come from now")
	}
}

func TestStandTimingDefaultsAndValidation(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	if c.StandTiming != eventStandModel.DefaultTiming() {
		t.Fatalf("stand timing default = %+v", c.StandTiming)
	}
	if err := c.SetStandTiming(eventStandModel.Timing{DeployLeadMinutes: 45, TeardownDelayMinutes: 0}, cfgNow.Add(time.Minute), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if c.StandTiming.DeployLeadMinutes != 45 || c.StandTiming.TeardownDelayMinutes != 0 || !c.UpdatedAt.Equal(cfgNow.Add(time.Minute)) {
		t.Fatalf("stand timing not applied: %+v", c)
	}
	if err := c.SetStandTiming(eventStandModel.Timing{DeployLeadMinutes: 1}, cfgNow.Add(2*time.Minute), uuid.Nil); !errors.Is(err, eventStandModel.ErrStandSettingsInvalid.Err()) {
		t.Fatalf("invalid lead accepted: %v", err)
	}
	if c.StandTiming.DeployLeadMinutes != 45 {
		t.Fatal("a rejected timing must leave the config untouched")
	}
}

func TestSetThemeDerivesReadableAccentsAndVersions(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	actor := uuid.Must(uuid.NewV7())
	later := cfgNow.Add(time.Hour)
	if err := c.SetTheme("#0B120E", "#5CCB7C", later, actor); err != nil {
		t.Fatalf("set theme: %v", err)
	}
	if c.Theme.Brand != "#0B120E" || c.Theme.Accent != "#5CCB7C" || c.Theme.AccentLight != "#397E4D" || c.Theme.AccentDark != "#5CCB7C" || c.Theme.AccentLive != "#5CCB7C" {
		t.Fatalf("unexpected derived theme: %+v", c.Theme)
	}
	if c.Theme.Version != 2 || !c.UpdatedAt.Equal(later) || !c.UpdatedBy.Valid || c.UpdatedBy.UUID != actor {
		t.Fatalf("theme edit must increment version and touch config: %+v", c)
	}
	if err := c.SetTheme("#0B120E", "#5CCB7C", later.Add(time.Hour), actor); err != nil {
		t.Fatalf("same theme must be idempotent: %v", err)
	}
	if c.Theme.Version != 2 || !c.UpdatedAt.Equal(later) {
		t.Fatal("idempotent theme update changed version or timestamp")
	}
}

func TestSetThemeRejectsInvalidColorsWithoutMutation(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	before := c.Theme
	for _, input := range [][2]string{{"#12345", ""}, {"#211A52", "green"}} {
		if err := c.SetTheme(input[0], input[1], cfgNow.Add(time.Hour), uuid.Nil); err == nil {
			t.Fatalf("expected invalid color error for %q, %q", input[0], input[1])
		}
		if c.Theme != before || !c.UpdatedAt.Equal(cfgNow) {
			t.Fatal("invalid theme mutated config")
		}
	}
}

func TestSetThemeAllowsLowContrastBrand(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	if err := c.SetTheme("#FFFFFF", "", cfgNow.Add(time.Hour), uuid.Nil); err != nil {
		t.Fatalf("valid color must remain saveable despite low white-text contrast: %v", err)
	}
	if c.Theme.Brand != "#FFFFFF" {
		t.Fatalf("brand not saved: %+v", c.Theme)
	}
}

func TestSetParticipationChangesUntilPublication(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	actor := uuid.Must(uuid.NewV7())

	if err := c.SetParticipation(eventConfigModel.ParticipationTeam, false, cfgNow, actor); err != nil {
		t.Fatalf("first set must succeed: %v", err)
	}
	if c.Participation == nil || *c.Participation != eventConfigModel.ParticipationTeam {
		t.Fatalf("participation not stored")
	}
	// Re-setting the SAME value is an idempotent no-op (no error).
	if err := c.SetParticipation(eventConfigModel.ParticipationTeam, true, cfgNow, actor); err != nil {
		t.Fatalf("re-set same value must be a no-op, got %v", err)
	}
	if err := c.SetParticipation(eventConfigModel.ParticipationIndividual, false, cfgNow, actor); err != nil {
		t.Fatalf("changing participation before publication must succeed: %v", err)
	}
	if err := c.SetParticipation(eventConfigModel.ParticipationTeam, true, cfgNow, actor); err == nil {
		t.Fatalf("changing participation after publication must error")
	}
}

func TestSetParticipationInvalid(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	if err := c.SetParticipation(eventConfigModel.Participation(9), false, cfgNow, uuid.Nil); err == nil {
		t.Fatalf("out-of-range participation must error")
	}
}

func TestUpdateAppliesAndValidates(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	later := cfgNow.Add(time.Hour)
	actor := uuid.Must(uuid.NewV7())

	if err := c.Update(newValidInput(), later, actor); err != nil {
		t.Fatalf("valid update: %v", err)
	}
	if c.Registration != eventConfigModel.RegistrationApproval {
		t.Fatalf("mutable fields not applied")
	}
	if !c.UpdatedAt.Equal(later) || !c.UpdatedBy.Valid || c.UpdatedBy.UUID != actor {
		t.Fatalf("UpdatedAt/By not touched")
	}
	// Update must NEVER touch participation.
	if c.Participation != nil {
		t.Fatalf("Update must not set participation")
	}

	bad := newValidInput()
	bad.Registration = eventConfigModel.Registration(7)
	if err := c.Update(bad, later, actor); err == nil {
		t.Fatalf("invalid registration must error")
	}

	bad = newValidInput()
	bad.ScoreboardVisibility = eventConfigModel.Visibility(7)
	if err := c.Update(bad, later, actor); err == nil {
		t.Fatalf("invalid visibility must error")
	}

	bad = newValidInput()
	bad.PreviewDescription = strings.Repeat("x", 1001)
	if err := c.Update(bad, later, actor); err == nil {
		t.Fatalf("too-long preview description must error")
	}

	bad = newValidInput()
	bad.PreviewPicture = "http://insecure.test/x.png" // not https
	if err := c.Update(bad, later, actor); err == nil {
		t.Fatalf("non-https preview picture must error")
	}
	// Empty preview picture is allowed.
	ok := newValidInput()
	ok.PreviewPicture = ""
	if err := c.Update(ok, later, actor); err != nil {
		t.Fatalf("empty preview picture must be allowed: %v", err)
	}
}

func TestEffectiveMinTeamSize(t *testing.T) {
	team, individual := eventConfigModel.ParticipationTeam, eventConfigModel.ParticipationIndividual
	three := int32(3)
	cases := []struct {
		name string
		cfg  eventConfigModel.EventConfig
		want int32
	}{
		{"team default", eventConfigModel.EventConfig{Participation: &team, MaxTeamSize: 5}, 2},
		{"team default capped", eventConfigModel.EventConfig{Participation: &team, MaxTeamSize: 1}, 1},
		{"configured", eventConfigModel.EventConfig{Participation: &team, MaxTeamSize: 5, MinTeamSize: &three}, 3},
		{"individual", eventConfigModel.EventConfig{Participation: &individual, MaxTeamSize: 5}, 1},
		{"unset", eventConfigModel.EventConfig{MaxTeamSize: 5}, 1},
	}
	for _, tc := range cases {
		if got := tc.cfg.EffectiveMinTeamSize(); got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}

func TestBoardPresentationDefaultsAndUpdates(t *testing.T) {
	c := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	if !c.ShowDifficulty || c.HintsDisabled {
		t.Fatalf("difficulty must default on and hints must not be disabled: %+v", c)
	}
	in := newValidInput()
	in.ShowDifficulty, in.HintsDisabled = false, true
	if err := c.Update(in, cfgNow.Add(time.Minute), uuid.Must(uuid.NewV7())); err != nil {
		t.Fatalf("update: %v", err)
	}
	if c.ShowDifficulty || !c.HintsDisabled {
		t.Fatalf("board presentation not applied: %+v", c)
	}
}

func TestTaskRevealModeIsChosenBeforeTheStart(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	by := uuid.Must(uuid.NewV7())
	cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), now)
	if cfg.TaskRevealMode != eventConfigModel.RevealAllReady {
		t.Fatalf("default mode = %q, want all_ready", cfg.TaskRevealMode)
	}
	later := now.Add(time.Hour)
	if err := cfg.SetTaskRevealMode(eventConfigModel.RevealAsReady, false, later, by); err != nil || cfg.TaskRevealMode != eventConfigModel.RevealAsReady || !cfg.UpdatedAt.Equal(later) {
		t.Fatalf("before the start: %v mode=%q updated=%v", err, cfg.TaskRevealMode, cfg.UpdatedAt)
	}
	// After the start the current value is accepted (no change), another one is locked.
	untouched := cfg.UpdatedAt
	if err := cfg.SetTaskRevealMode(eventConfigModel.RevealAsReady, true, later.Add(time.Hour), by); err != nil || !cfg.UpdatedAt.Equal(untouched) {
		t.Fatalf("same value after the start: %v", err)
	}
	if err := cfg.SetTaskRevealMode(eventConfigModel.RevealAllReady, true, later.Add(time.Hour), by); !errors.Is(err, eventConfigModel.ErrTaskRevealModeLocked.Err()) || cfg.TaskRevealMode != eventConfigModel.RevealAsReady {
		t.Fatalf("after the start: %v mode=%q", err, cfg.TaskRevealMode)
	}
	for _, bad := range []eventConfigModel.TaskRevealMode{"", "gradual", "ALL_READY"} {
		if err := cfg.SetTaskRevealMode(bad, false, later, by); !errors.Is(err, eventConfigModel.ErrTaskRevealModeInvalid.Err()) {
			t.Fatalf("mode %q: %v, want invalid", bad, err)
		}
	}
}
