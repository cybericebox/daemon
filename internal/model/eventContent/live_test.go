package eventContentModel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultLiveLayoutShowsTheTimer(t *testing.T) {
	layout := DefaultLiveLayout()
	for _, widget := range layout.Widgets {
		if widget.Type == "timer" {
			return
		}
	}
	t.Fatal("the default live layout must show the timer")
}

func TestLiveRefreshSecondsIsOptionalAndBounded(t *testing.T) {
	for _, seconds := range []int{0, 2, 5, 30} {
		layout := DefaultLiveLayout()
		layout.RefreshSeconds = seconds
		if err := layout.Validate(); err != nil {
			t.Fatalf("refresh %d s rejected: %v", seconds, err)
		}
	}
	for _, seconds := range []int{-1, 1, 31, 600} {
		layout := DefaultLiveLayout()
		layout.RefreshSeconds = seconds
		if err := layout.Validate(); err == nil {
			t.Fatalf("refresh %d s accepted", seconds)
		}
	}
}

func TestLiveRefreshSecondsDefaultsForStoredLayouts(t *testing.T) {
	if got := (LiveLayout{}).Refresh(); got != LiveRefreshDefault {
		t.Fatalf("a layout without refreshSeconds must refresh every %d s, got %d", LiveRefreshDefault, got)
	}
	if got := (LiveLayout{RefreshSeconds: 12}).Refresh(); got != 12 {
		t.Fatalf("refresh = %d, want 12", got)
	}
}

func TestLiveTimerSettings(t *testing.T) {
	layout := DefaultLiveLayout()
	for index := range layout.Widgets {
		if layout.Widgets[index].Type == "timer" {
			layout.Widgets[index].Props = map[string]json.RawMessage{"showLabel": json.RawMessage(`false`), "showSeconds": json.RawMessage(`true`)}
		}
	}
	if err := layout.Validate(); err != nil {
		t.Fatalf("timer settings rejected: %v", err)
	}
	for index := range layout.Widgets {
		if layout.Widgets[index].Type == "timer" {
			layout.Widgets[index].Props = map[string]json.RawMessage{"showSeconds": json.RawMessage(`"yes"`)}
		}
	}
	if err := layout.Validate(); err == nil {
		t.Fatal("a non-boolean timer setting was accepted")
	}
}

func timerLayout(props map[string]json.RawMessage) LiveLayout {
	layout := DefaultLiveLayout()
	for index := range layout.Widgets {
		if layout.Widgets[index].Type == "timer" {
			layout.Widgets[index].Props = props
		}
	}
	return layout
}

func TestLiveTimerSourceLabelAndSize(t *testing.T) {
	valid := []map[string]json.RawMessage{
		{"source": json.RawMessage(`"auto"`)},
		{"source": json.RawMessage(`"freeze"`), "label": json.RawMessage(`"До заморожування"`), "labelSize": json.RawMessage(`"l"`)},
		{"source": json.RawMessage(`"custom"`), "target": json.RawMessage(`"2026-10-01T18:00:00Z"`)},
		{"label": json.RawMessage(`""`), "labelSize": json.RawMessage(`"s"`)},
	}
	for _, props := range valid {
		if err := timerLayout(props).Validate(); err != nil {
			t.Fatalf("timer %v rejected: %v", props, err)
		}
	}
	invalid := []map[string]json.RawMessage{
		{"source": json.RawMessage(`"lunch"`)},
		{"source": json.RawMessage(`"custom"`)},
		{"source": json.RawMessage(`"custom"`), "target": json.RawMessage(`"tomorrow"`)},
		{"labelSize": json.RawMessage(`"xl"`)},
		{"label": json.RawMessage(`"` + strings.Repeat("я", 61) + `"`)},
	}
	for _, props := range invalid {
		if err := timerLayout(props).Validate(); err == nil {
			t.Fatalf("timer %v accepted", props)
		}
	}
}

func widgetLayout(kind string, props map[string]json.RawMessage) LiveLayout {
	layout := DefaultLiveLayout()
	layout.Widgets = []LiveWidget{{ID: "w", Type: kind, X: 1, Y: 1, W: 4, H: 2, Props: props}}
	return layout
}

func TestLiveLogoItemsAndColour(t *testing.T) {
	ok := map[string]json.RawMessage{
		"items": json.RawMessage(`[{"src":"/api/events/e/content-images/a"},{"src":"/api/events/e/content-images/b","dark":"/api/events/e/content-images/c"}]`),
		"color": json.RawMessage(`"mono"`),
	}
	if err := widgetLayout("logos", ok).Validate(); err != nil {
		t.Fatalf("logo items rejected: %v", err)
	}
	for _, props := range []map[string]json.RawMessage{
		{"items": json.RawMessage(`[{"src":"javascript:alert(1)"}]`)},
		{"items": json.RawMessage(`[{"src":"/a","dark":"http://plain.example/x.png"}]`)},
		{"items": json.RawMessage(`[{"dark":"/a"}]`)},
		{"items": json.RawMessage(`[{"src":"/a","extra":1}]`)},
		{"color": json.RawMessage(`"rainbow"`)},
	} {
		if err := widgetLayout("logos", props).Validate(); err == nil {
			t.Fatalf("logos %v accepted", props)
		}
	}
}

func TestLiveQRValueAndCaption(t *testing.T) {
	for _, value := range []string{`"https://ctf.example/register"`, `"/register"`, `"WIFI:S:ctf;T:WPA;P:secret;;"`, `"Будь-який текст"`} {
		if err := widgetLayout("qr", map[string]json.RawMessage{"value": json.RawMessage(value), "caption": json.RawMessage(`"Реєстрація"`)}).Validate(); err != nil {
			t.Fatalf("qr value %s rejected: %v", value, err)
		}
	}
	for _, props := range []map[string]json.RawMessage{
		{"value": json.RawMessage(`"https://"`)},
		{"value": json.RawMessage(`""`)},
		{"value": json.RawMessage(`"` + strings.Repeat("x", 501) + `"`)},
		{"caption": json.RawMessage(`"` + strings.Repeat("я", 61) + `"`)},
	} {
		if err := widgetLayout("qr", props).Validate(); err == nil {
			t.Fatalf("qr %v accepted", props)
		}
	}
}

func TestLiveFormatsAutoAndCustom(t *testing.T) {
	layout := DefaultLiveLayout()
	layout.Formats = map[string]LiveFormat{
		"16:10": {Mode: "auto"},
		"4:3": {Mode: "custom", Grid: &LiveGrid{Cols: 8, Rows: 10}, Placements: []LivePlacement{
			{ID: "title", X: 1, Y: 1, W: 6, H: 1}, {ID: "timer", X: 7, Y: 1, W: 2, H: 1},
			{ID: "chart", X: 1, Y: 2, W: 8, H: 4}, {ID: "table", X: 1, Y: 6, W: 8, H: 3},
			{ID: "organizers", X: 1, Y: 9, W: 3, H: 2}, {ID: "partners", X: 4, Y: 9, W: 5, H: 2},
		}},
	}
	if err := layout.Validate(); err != nil {
		t.Fatalf("formats rejected: %v", err)
	}
	custom := func(mutate func(*LiveFormat)) LiveLayout {
		next := DefaultLiveLayout()
		format := LiveFormat{Mode: "custom", Grid: &LiveGrid{Cols: 12, Rows: 8}, Placements: []LivePlacement{{ID: "chart", X: 1, Y: 2, W: 8, H: 6}}}
		mutate(&format)
		next.Formats = map[string]LiveFormat{"4:3": format}
		return next
	}
	invalid := map[string]LiveLayout{
		"base aspect": func() LiveLayout {
			l := DefaultLiveLayout()
			l.Formats = map[string]LiveFormat{"16:9": {Mode: "auto"}}
			return l
		}(),
		"unknown format": func() LiveLayout {
			l := DefaultLiveLayout()
			l.Formats = map[string]LiveFormat{"21:9": {Mode: "auto"}}
			return l
		}(),
		"unknown mode": func() LiveLayout {
			l := DefaultLiveLayout()
			l.Formats = map[string]LiveFormat{"4:3": {Mode: "magic"}}
			return l
		}(),
		"auto with cells": func() LiveLayout {
			l := DefaultLiveLayout()
			l.Formats = map[string]LiveFormat{"4:3": {Mode: "auto", Grid: &LiveGrid{Cols: 12, Rows: 8}}}
			return l
		}(),
		"no grid":        custom(func(f *LiveFormat) { f.Grid = nil }),
		"foreign widget": custom(func(f *LiveFormat) { f.Placements = []LivePlacement{{ID: "ghost", X: 1, Y: 1, W: 4, H: 3}} }),
		"twice":          custom(func(f *LiveFormat) { f.Placements = append(f.Placements, f.Placements[0]) }),
		"too small":      custom(func(f *LiveFormat) { f.Placements = []LivePlacement{{ID: "chart", X: 1, Y: 1, W: 2, H: 2}} }),
		"outside":        custom(func(f *LiveFormat) { f.Placements = []LivePlacement{{ID: "chart", X: 10, Y: 1, W: 4, H: 3}} }),
		"overlap": custom(func(f *LiveFormat) {
			f.Placements = []LivePlacement{{ID: "chart", X: 1, Y: 1, W: 4, H: 3}, {ID: "table", X: 2, Y: 2, W: 3, H: 3}}
		}),
	}
	for name, layout := range invalid {
		if err := layout.Validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
