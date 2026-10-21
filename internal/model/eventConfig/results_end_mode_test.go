package eventConfigModel_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/gofrs/uuid"
)

func resultsWithEndMode(t *testing.T, mode string) eventConfigModel.ResultsSettings {
	t.Helper()
	settings := eventConfigModel.DefaultResultsSettings()
	if err := json.Unmarshal([]byte(`{"FreezeEnabled":true,"FreezeMinutes":30,"FreezeEndMode":"`+mode+`"}`), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestResultsFreezeEndModeWindows(t *testing.T) {
	finish := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	for _, mode := range []string{"", "at_finish", "manual"} {
		t.Run(mode, func(t *testing.T) {
			settings := resultsWithEndMode(t, mode)
			for _, state := range []struct {
				at   time.Time
				want bool
			}{
				{finish.Add(-31 * time.Minute), false}, {finish.Add(-30 * time.Minute), true},
				{finish.Add(-time.Minute), true}, {finish, mode == "manual"}, {finish.Add(24 * time.Hour), mode == "manual"},
			} {
				if got := settings.FreezeActive(&finish, state.at); got != state.want {
					t.Errorf("%s: active=%t want=%t", state.at, got, state.want)
				}
			}
			if settings.FreezeActive(nil, finish) {
				t.Error("missing finish must not start a freeze")
			}
			settings.FreezeEnabled = false
			if settings.FreezeActive(&finish, finish) {
				t.Error("disabled freeze must stay inactive")
			}
		})
	}
}

func TestManualResultsFreezeOpensAndRestoresAfterFinish(t *testing.T) {
	finish := cfgNow.Add(-time.Hour)
	cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	by := uuid.Must(uuid.NewV7())
	if err := cfg.SetResultsSettings(eventConfigModel.VisibilityPublic, resultsWithEndMode(t, "manual"), cfgNow, by); err != nil {
		t.Fatal(err)
	}
	if !cfg.Results.FreezeActive(&finish, cfgNow) {
		t.Error("manual freeze must continue after finish")
	}
	cfg.SetResultsOpened(true, cfgNow, by)
	if cfg.Results.FreezeActive(&finish, cfgNow) {
		t.Error("manual opening must end the freeze")
	}
	cfg.SetResultsOpened(true, cfgNow.Add(time.Minute), by)
	if cfg.Results.OpenedAt == nil || !cfg.Results.OpenedAt.Equal(cfgNow) {
		t.Error("repeated opening must keep its first timestamp")
	}
	cfg.SetResultsOpened(false, cfgNow.Add(2*time.Minute), by)
	if !cfg.Results.FreezeActive(&finish, cfgNow.Add(2*time.Minute)) {
		t.Error("reset must restore manual freeze after finish")
	}
}

func TestResultsFreezeEndModeDefaultsAndValidation(t *testing.T) {
	cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
	body, err := json.Marshal(cfg.Results)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["FreezeEndMode"]) != `"at_finish"` {
		t.Errorf("default mode=%s", fields["FreezeEndMode"])
	}
	if err := cfg.SetResultsSettings(eventConfigModel.VisibilityPublic, resultsWithEndMode(t, "unknown"), cfgNow, uuid.Must(uuid.NewV7())); !errors.Is(err, eventConfigModel.ErrResultsSettingsInvalid.Err()) {
		t.Errorf("unknown mode accepted: %v", err)
	}
}

func TestResultsFreezeBannerValidationAndNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, text, level string
		valid             bool
	}{
		{"default", "", "", true}, {"info", "  Підсумки згодом.  ", "info", true},
		{"warning", strings.Repeat("я", 280), "warning", true}, {"critical", "Зачекайте на підсумки.", "critical", true},
		{"too long", strings.Repeat("я", 281), "warning", false}, {"unknown level", "Підсумки", "danger", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := eventConfigModel.NewEventConfig(uuid.Must(uuid.NewV7()), cfgNow)
			settings := eventConfigModel.DefaultResultsSettings()
			body, err := json.Marshal(map[string]string{"FreezeBannerText": tc.text, "FreezeBannerLevel": tc.level})
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &settings); err != nil {
				t.Fatal(err)
			}
			err = cfg.SetResultsSettings(eventConfigModel.VisibilityPublic, settings, cfgNow, uuid.Must(uuid.NewV7()))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if !tc.valid {
				return
			}
			body, err = json.Marshal(cfg.Results)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			wantText, _ := json.Marshal(strings.TrimSpace(tc.text))
			wantLevel := tc.level
			if wantLevel == "" {
				wantLevel = "warning"
			}
			if string(fields["FreezeBannerText"]) != string(wantText) || string(fields["FreezeBannerLevel"]) != `"`+wantLevel+`"` {
				t.Fatalf("banner not preserved/canonical: %s", body)
			}
		})
	}
}
