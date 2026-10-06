package event

import (
	"strings"
	"testing"
)

func TestParseAnswerFilters(t *testing.T) {
	filters, err := ParseAnswerFilters(`[{"Key":" city ","Op":"contains","Value":" Київ "},{"Key":"langs","Op":"any","Values":["Go","Rust"]},{"Key":"agree","Op":"bool","Value":false},{"Key":"cv","Op":"present","Value":true}]`)
	if err != nil {
		t.Fatalf("ParseAnswerFilters: %v", err)
	}
	got := string(answerFiltersJSON(filters))
	want := `[{"key":"city","op":"contains","value":"Київ"},{"key":"langs","op":"any","values":["Go","Rust"]},{"key":"agree","op":"bool","value":false},{"key":"cv","op":"present","value":true}]`
	if got != want {
		t.Fatalf("filters JSON = %s, want %s", got, want)
	}
}

func TestParseAnswerFiltersEmpty(t *testing.T) {
	filters, err := ParseAnswerFilters("  ")
	if err != nil || filters != nil {
		t.Fatalf("empty = %v %v", filters, err)
	}
	if got := string(answerFiltersJSON(nil)); got != "[]" {
		t.Fatalf("empty JSON = %s", got)
	}
}

func TestParseAnswerFiltersRejectsInvalid(t *testing.T) {
	many := "[" + strings.TrimSuffix(strings.Repeat(`{"Key":"a","Op":"bool","Value":true},`, maxAnswerFilters+1), ",") + "]"
	for name, raw := range map[string]string{
		"not json":       `{`,
		"unknown op":     `[{"Key":"a","Op":"regex","Value":"x"}]`,
		"empty key":      `[{"Key":" ","Op":"bool","Value":true}]`,
		"blank contains": `[{"Key":"a","Op":"contains","Value":"  "}]`,
		"long contains":  `[{"Key":"a","Op":"contains","Value":"` + strings.Repeat("x", maxAnswerFilterText+1) + `"}]`,
		"no values":      `[{"Key":"a","Op":"any","Values":[]}]`,
		"bool as text":   `[{"Key":"a","Op":"bool","Value":"yes"}]`,
		"too many":       many,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAnswerFilters(raw); err == nil {
				t.Fatalf("want error for %s", raw)
			}
		})
	}
}

func TestParseAnswerFiltersRange(t *testing.T) {
	filters, err := ParseAnswerFilters(`[{"Key":"@date","Op":"range","Type":"date","From":"2026-09-01T00:00:00+03:00"},{"Key":"age","Op":"range","Type":"number","From":18,"To":30}]`)
	if err != nil {
		t.Fatalf("ParseAnswerFilters: %v", err)
	}
	want := `[{"key":"@date","op":"range","type":"date","from":"2026-08-31T21:00:00Z"},{"key":"age","op":"range","type":"number","from":18,"to":30}]`
	if got := string(answerFiltersJSON(filters)); got != want {
		t.Fatalf("range JSON = %s, want %s", got, want)
	}
	for name, raw := range map[string]string{
		"no bounds":          `[{"Key":"age","Op":"range","Type":"number"}]`,
		"exclusive date":     `[{"Key":"@date","Op":"range","Type":"date","From":"2026-09-01T00:00:00Z","FromExclusive":true}]`,
		"bad date":           `[{"Key":"@date","Op":"range","Type":"date","From":"yesterday"}]`,
		"text as number":     `[{"Key":"age","Op":"range","Type":"number","From":"18"}]`,
		"unknown range type": `[{"Key":"age","Op":"range","Type":"money","From":1}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAnswerFilters(raw); err == nil {
				t.Fatalf("want error for %s", raw)
			}
		})
	}
}

func TestParseAnswerFiltersTypedRanges(t *testing.T) {
	filters, err := ParseAnswerFilters(`[{"Key":"@members","Op":"range","Type":"number","From":3,"FromExclusive":true},{"Key":"age","Op":"range","Type":"number","To":18,"ToExclusive":true,"FromExclusive":true},{"Key":"born","Op":"range","Type":"date","From":"2000-01-01T00:00:00Z"}]`)
	if err != nil {
		t.Fatalf("ParseAnswerFilters: %v", err)
	}
	want := `[{"key":"@members","op":"range","type":"number","from":3,"fromExclusive":true},{"key":"age","op":"range","type":"number","to":18,"toExclusive":true},{"key":"born","op":"range","type":"date","from":"2000-01-01T00:00:00Z"}]`
	if got := string(answerFiltersJSON(filters)); got != want {
		t.Fatalf("typed ranges JSON = %s, want %s", got, want)
	}
}

func TestParseAnswerFiltersDateAndTimeBounds(t *testing.T) {
	filters, err := ParseAnswerFilters(`[{"Key":"born","Op":"range","Type":"date","From":"2000-01-31","To":"2000-12-31"},{"Key":"slot","Op":"range","Type":"time","From":"09:00","To":"18:30"}]`)
	if err != nil {
		t.Fatalf("ParseAnswerFilters: %v", err)
	}
	want := `[{"key":"born","op":"range","type":"date","from":"2000-01-31","to":"2000-12-31"},{"key":"slot","op":"range","type":"time","from":"09:00","to":"18:30"}]`
	if got := string(answerFiltersJSON(filters)); got != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
	for _, raw := range []string{
		`[{"Key":"slot","Op":"range","Type":"time","From":"9:00"}]`,
		`[{"Key":"slot","Op":"range","Type":"time","From":"24:00"}]`,
		`[{"Key":"slot","Op":"range","Type":"time","From":"09:00","FromExclusive":true}]`,
		`[{"Key":"born","Op":"range","Type":"date","From":"2000-02-30"}]`,
	} {
		if _, err := ParseAnswerFilters(raw); err == nil {
			t.Fatalf("want error for %s", raw)
		}
	}
}
