package platformSettingsModel_test

import (
	"errors"
	"testing"

	appErr "github.com/cybericebox/daemon/pkg/err"

	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
)

var specs = []platformSettingsModel.FieldSpec{
	{Key: "A", Kind: platformSettingsModel.KindDuration, Min: 10, Max: 100},
	{Key: "B", Kind: platformSettingsModel.KindCount, Min: 1, Max: 5},
}

// publicContext returns the public context of an application error.
func publicContext(t *testing.T, e error) map[string]any {
	t.Helper()
	var ae appErr.Error
	if !errors.As(e, &ae) {
		t.Fatalf("not an application error: %v", e)
	}
	return ae.PublicContext()
}

func TestIsTypedKey(t *testing.T) {
	for _, key := range []string{platformSettingsModel.RetentionKey, platformSettingsModel.ArchiveLimitsKey, platformSettingsModel.TestRunsKey} {
		if !platformSettingsModel.IsTypedKey(key) {
			t.Errorf("%s must be typed", key)
		}
	}
	if platformSettingsModel.IsTypedKey("platform_name") {
		t.Error("a generic key is not typed")
	}
	if platformSettingsModel.RetentionKey != "retention_policy" || platformSettingsModel.ArchiveLimitsKey != "exercise_archive_limits" ||
		platformSettingsModel.TestRunsKey != "exercise_test_runs" {
		t.Error("typed keys changed: they are stored row keys")
	}
}

func TestValidateValues(t *testing.T) {
	cases := []struct {
		name    string
		values  platformSettingsModel.Values
		want    error
		context map[string]any
	}{
		{"at the lower bounds", platformSettingsModel.Values{"A": 10, "B": 1}, nil, nil},
		{"at the upper bounds", platformSettingsModel.Values{"A": 100, "B": 5}, nil, nil},
		{"missing", platformSettingsModel.Values{"A": 10}, platformSettingsModel.ErrSettingValueMissing.Err(), map[string]any{"field": "B"}},
		{"unknown", platformSettingsModel.Values{"A": 10, "B": 1, "C": 1}, platformSettingsModel.ErrSettingUnknownField.Err(), map[string]any{"field": "C"}},
		{"several unknown: the first in order", platformSettingsModel.Values{"Z": 1, "Y": 1, "D": 1, "X": 1, "E": 1},
			platformSettingsModel.ErrSettingUnknownField.Err(), map[string]any{"field": "D"}},
		{"below min", platformSettingsModel.Values{"A": 9, "B": 1}, platformSettingsModel.ErrSettingValueOutOfRange.Err(),
			map[string]any{"field": "A", "min": int64(10), "max": int64(100)}},
		{"above max", platformSettingsModel.Values{"A": 10, "B": 6}, platformSettingsModel.ErrSettingValueOutOfRange.Err(),
			map[string]any{"field": "B", "min": int64(1), "max": int64(5)}},
		{"zero", platformSettingsModel.Values{"A": 0, "B": 1}, platformSettingsModel.ErrSettingValueOutOfRange.Err(),
			map[string]any{"field": "A", "min": int64(10), "max": int64(100)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := platformSettingsModel.ValidateValues(specs, tc.values)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("unexpected error: %v", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			ctx := publicContext(t, got)
			for k, v := range tc.context {
				if ctx[k] != v {
					t.Errorf("public context %s: got %v, want %v", k, ctx[k], v)
				}
			}
		})
	}
}

func TestValidateValues_ErrorsAreInvalidData(t *testing.T) {
	got := platformSettingsModel.ValidateValues(specs, platformSettingsModel.Values{})
	if !errors.Is(got, appErr.ErrInvalidData.Err()) {
		t.Fatalf("a refused value is a 400: %v", got)
	}
}

func TestOverlay(t *testing.T) {
	base := platformSettingsModel.Values{"A": 50, "B": 3}
	got := platformSettingsModel.Overlay(base, platformSettingsModel.Values{"B": 4, "C": 9}, specs)
	if len(got) != 2 || got["A"] != 50 || got["B"] != 4 {
		t.Fatalf("overlay: %v", got)
	}
	if base["B"] != 3 {
		t.Fatal("overlay must not change the base")
	}
	if got := platformSettingsModel.Overlay(base, nil, specs); got["A"] != 50 || got["B"] != 3 {
		t.Fatalf("nothing stored keeps the base: %v", got)
	}
}
