package eventContentModel

import (
	"encoding/json"
	"fmt"
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
		{"source": json.RawMessage(`"stage-end"`)},
		{"source": json.RawMessage(`"stage-next"`)},
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

func TestLiveTablePinnedRowsAndStageSubtitle(t *testing.T) {
	tableLayout := func(props map[string]json.RawMessage) LiveLayout {
		layout := widgetLayout("table", props)
		layout.Widgets[0].H = 3
		return layout
	}
	for _, value := range []string{"0", "3", "5"} {
		if err := tableLayout(map[string]json.RawMessage{"pinTop": json.RawMessage(value)}).Validate(); err != nil {
			t.Fatalf("pinTop %s rejected: %v", value, err)
		}
	}
	for _, value := range []string{"4", "-1", `"3"`} {
		if err := tableLayout(map[string]json.RawMessage{"pinTop": json.RawMessage(value)}).Validate(); err == nil {
			t.Fatalf("pinTop %s accepted", value)
		}
	}
	if err := widgetLayout("title", map[string]json.RawMessage{"subtitleStage": json.RawMessage(`true`)}).Validate(); err != nil {
		t.Fatalf("stage subtitle rejected: %v", err)
	}
}

func TestLiveSafeArea(t *testing.T) {
	for _, value := range []float64{0, 2.5, 5} {
		layout := DefaultLiveLayout()
		layout.Screen.SafeArea = value
		if err := layout.Validate(); err != nil {
			t.Fatalf("safe area %v rejected: %v", value, err)
		}
	}
	for _, value := range []float64{1, 10, -2.5} {
		layout := DefaultLiveLayout()
		layout.Screen.SafeArea = value
		if err := layout.Validate(); err == nil {
			t.Fatalf("safe area %v accepted", value)
		}
	}
}

func TestDefaultLiveLayoutIsOneScreenForAllStates(t *testing.T) {
	layout := DefaultLiveLayout()
	if err := layout.Validate(); err != nil {
		t.Fatalf("default layout rejected: %v", err)
	}
	if layout.Scenes != nil {
		t.Fatalf("the default layout shows one screen in every state, got scenes %v", layout.Scenes)
	}
}

func sceneLayout(key string, scene LiveScene) LiveLayout {
	layout := DefaultLiveLayout()
	layout.Scenes = map[string]LiveScene{key: scene}
	return layout
}

func TestLiveScenesFollowTheCanvasRules(t *testing.T) {
	podium := LiveWidget{ID: "p", Type: "podium", X: 1, Y: 1, W: 12, H: 4, Props: map[string]json.RawMessage{}}
	for _, key := range []string{"before", "frozen", "finished"} {
		if err := sceneLayout(key, LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{podium}}).Validate(); err != nil {
			t.Fatalf("scene %s rejected: %v", key, err)
		}
	}
	// An empty scene is a valid blank screen.
	if err := sceneLayout("frozen", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}}).Validate(); err != nil {
		t.Fatalf("empty scene rejected: %v", err)
	}
	invalid := map[string]LiveLayout{
		"running is the layout itself": sceneLayout("running", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}}),
		"unknown state":                sceneLayout("lunch", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}}),
		"no grid":                      sceneLayout("before", LiveScene{Widgets: []LiveWidget{podium}}),
		"outside the grid":             sceneLayout("before", LiveScene{Grid: LiveGrid{Cols: 8, Rows: 8}, Widgets: []LiveWidget{podium}}),
		"overlap": sceneLayout("before", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{podium,
			{ID: "q", Type: "qr", X: 2, Y: 2, W: 1, H: 1, Props: map[string]json.RawMessage{}}}}),
		"repeated id": sceneLayout("before", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{podium,
			{ID: "p", Type: "qr", X: 1, Y: 6, W: 1, H: 1, Props: map[string]json.RawMessage{}}}}),
		"bad props": sceneLayout("finished", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{
			{ID: "p", Type: "podium", X: 1, Y: 1, W: 12, H: 4, Props: map[string]json.RawMessage{"showPoints": json.RawMessage(`"yes"`)}}}}),
		"format of another scene's widget": sceneLayout("before", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{podium},
			Formats: map[string]LiveFormat{"4:3": {Mode: "custom", Grid: &LiveGrid{Cols: 8, Rows: 10}, Placements: []LivePlacement{{ID: "chart", X: 1, Y: 1, W: 8, H: 4}}}}}),
		"format of the base shape": sceneLayout("before", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Formats: map[string]LiveFormat{"16:9": {Mode: "auto"}}}),
	}
	for name, layout := range invalid {
		if err := layout.Validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// A scene's custom format places that scene's own widgets.
	ok := sceneLayout("before", LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{podium},
		Formats: map[string]LiveFormat{"4:3": {Mode: "custom", Grid: &LiveGrid{Cols: 8, Rows: 10}, Placements: []LivePlacement{{ID: "p", X: 1, Y: 1, W: 8, H: 4}}}}})
	if err := ok.Validate(); err != nil {
		t.Fatalf("scene format rejected: %v", err)
	}
}

func TestLiveNewWidgetsAndMinimums(t *testing.T) {
	valid := []LiveWidget{
		{ID: "podium", Type: "podium", X: 1, Y: 1, W: 4, H: 2, Props: map[string]json.RawMessage{"showPoints": json.RawMessage(`false`)}},
		{ID: "facts", Type: "facts", X: 5, Y: 1, W: 3, H: 1, Props: map[string]json.RawMessage{"items": json.RawMessage(`["teams","tasks","solves","solvedTasks","firstBlood","start"]`)}},
		{ID: "stages", Type: "stages", X: 5, Y: 2, W: 3, H: 2, Props: map[string]json.RawMessage{"showDone": json.RawMessage(`false`)}},
		{ID: "clock", Type: "clock", X: 8, Y: 1, W: 2, H: 1, Props: map[string]json.RawMessage{"showSeconds": json.RawMessage(`true`), "label": json.RawMessage(`"Київ"`), "align": json.RawMessage(`"center"`)}},
	}
	layout := DefaultLiveLayout()
	layout.Widgets = valid
	if err := layout.Validate(); err != nil {
		t.Fatalf("new widgets rejected: %v", err)
	}
	for _, widget := range []LiveWidget{
		{ID: "w", Type: "podium", X: 1, Y: 1, W: 3, H: 2},
		{ID: "w", Type: "facts", X: 1, Y: 1, W: 2, H: 1},
		{ID: "w", Type: "stages", X: 1, Y: 1, W: 3, H: 1},
		{ID: "w", Type: "clock", X: 1, Y: 1, W: 1, H: 1},
	} {
		small := DefaultLiveLayout()
		small.Widgets = []LiveWidget{widget}
		if err := small.Validate(); err == nil {
			t.Fatalf("%s below its minimum accepted", widget.Type)
		}
	}
	for _, props := range []map[string]json.RawMessage{
		{"items": json.RawMessage(`[]`)},
		{"items": json.RawMessage(`["teams","teams"]`)},
		{"items": json.RawMessage(`["salary"]`)},
		{"items": json.RawMessage(`["teams","tasks","solves","solvedTasks","firstBlood","start","finish"]`)},
		{"items": json.RawMessage(`"teams"`)},
	} {
		if err := widgetLayout("facts", props).Validate(); err == nil {
			t.Fatalf("facts %v accepted", props)
		}
	}
}

func TestLiveCommonWidgetSettings(t *testing.T) {
	for _, kind := range []string{"title", "timer", "chart", "table", "logos", "solves", "announcement", "qr", "podium", "facts", "stages", "clock"} {
		props := map[string]json.RawMessage{"textSize": json.RawMessage(`"xl"`), "showCaption": json.RawMessage(`false`), "caption": json.RawMessage(`"Рейтинг"`)}
		layout := widgetLayout(kind, props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err != nil {
			t.Fatalf("%s common settings rejected: %v", kind, err)
		}
	}
	for _, props := range []map[string]json.RawMessage{
		{"textSize": json.RawMessage(`"xxl"`)},
		{"textSize": json.RawMessage(`1.2`)},
		{"showCaption": json.RawMessage(`"no"`)},
		{"caption": json.RawMessage(`"` + strings.Repeat("я", 61) + `"`)},
	} {
		if err := widgetLayout("chart", map[string]json.RawMessage(props)).Validate(); err == nil {
			t.Fatalf("chart %v accepted", props)
		}
	}
}

func TestLiveBlockSettings(t *testing.T) {
	type check struct {
		kind  string
		props map[string]json.RawMessage
	}
	valid := []check{
		{"timer", map[string]json.RawMessage{"digits": json.RawMessage(`"words"`), "atZero": json.RawMessage(`"text"`), "zeroText": json.RawMessage(`"Час вийшов"`), "lastMinutes": json.RawMessage(`5`), "align": json.RawMessage(`"left"`)}},
		{"timer", map[string]json.RawMessage{"digits": json.RawMessage(`"compact"`), "atZero": json.RawMessage(`"hide"`), "lastMinutes": json.RawMessage(`0`)}},
		{"table", map[string]json.RawMessage{"columns": json.RawMessage(`2`), "limit": json.RawMessage(`8`), "offset": json.RawMessage(`3`)}},
		{"table", map[string]json.RawMessage{"columns": json.RawMessage(`1`), "limit": json.RawMessage(`0`), "offset": json.RawMessage(`0`)}},
		{"solves", map[string]json.RawMessage{"mode": json.RawMessage(`"first"`)}},
		{"title", map[string]json.RawMessage{"showLogo": json.RawMessage(`false`), "align": json.RawMessage(`"center"`)}},
		{"announcement", map[string]json.RawMessage{"align": json.RawMessage(`"left"`)}},
		{"logos", map[string]json.RawMessage{"rows": json.RawMessage(`3`)}},
	}
	for _, item := range valid {
		layout := widgetLayout(item.kind, item.props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err != nil {
			t.Fatalf("%s %v rejected: %v", item.kind, item.props, err)
		}
	}
	invalid := []check{
		{"timer", map[string]json.RawMessage{"digits": json.RawMessage(`"roman"`)}},
		{"timer", map[string]json.RawMessage{"atZero": json.RawMessage(`"explode"`)}},
		{"timer", map[string]json.RawMessage{"lastMinutes": json.RawMessage(`7`)}},
		{"timer", map[string]json.RawMessage{"zeroText": json.RawMessage(`"` + strings.Repeat("я", 61) + `"`)}},
		{"table", map[string]json.RawMessage{"columns": json.RawMessage(`3`)}},
		{"table", map[string]json.RawMessage{"limit": json.RawMessage(`61`)}},
		{"table", map[string]json.RawMessage{"limit": json.RawMessage(`-1`)}},
		{"table", map[string]json.RawMessage{"offset": json.RawMessage(`1`)}},
		{"solves", map[string]json.RawMessage{"mode": json.RawMessage(`"all"`)}},
		{"announcement", map[string]json.RawMessage{"align": json.RawMessage(`"justify"`)}},
		{"logos", map[string]json.RawMessage{"rows": json.RawMessage(`4`)}},
		// Settings of one type are not accepted on another.
		{"chart", map[string]json.RawMessage{"columns": json.RawMessage(`2`)}},
		{"qr", map[string]json.RawMessage{"align": json.RawMessage(`"left"`)}},
	}
	for _, item := range invalid {
		layout := widgetLayout(item.kind, item.props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err == nil {
			t.Fatalf("%s %v accepted", item.kind, item.props)
		}
	}
}

func TestLiveWidgetsOfAllScenesAreBounded(t *testing.T) {
	canvas := func(count int) LiveScene {
		widgets := make([]LiveWidget, 0, count)
		for i := range count {
			widgets = append(widgets, LiveWidget{ID: fmt.Sprintf("w%d", i), Type: "qr", X: i%12 + 1, Y: i/12 + 1, W: 1, H: 1, Props: map[string]json.RawMessage{}})
		}
		return LiveScene{Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: widgets}
	}
	layout := DefaultLiveLayout()
	layout.Widgets = canvas(48).Widgets
	layout.Scenes = map[string]LiveScene{"before": canvas(48)}
	if err := layout.Validate(); err != nil {
		t.Fatalf("96 widgets rejected: %v", err)
	}
	layout.Scenes["finished"] = canvas(1)
	if err := layout.Validate(); err == nil {
		t.Fatal("97 widgets in all scenes accepted")
	}
}

func TestLiveWidgetColours(t *testing.T) {
	// Every widget takes its fill, text and accent colour.
	for _, kind := range []string{"title", "timer", "chart", "table", "logos", "solves", "announcement", "qr", "podium", "facts", "stages", "clock"} {
		for _, fill := range []string{`"tile"`, `"brand"`, `"accent"`, `"#1A2b3C"`} {
			props := map[string]json.RawMessage{"background": json.RawMessage(fill), "textColor": json.RawMessage(`"#ffffff"`), "accentColor": json.RawMessage(`"#00C2FF"`)}
			layout := widgetLayout(kind, props)
			layout.Widgets[0].H = 3
			if err := layout.Validate(); err != nil {
				t.Fatalf("%s colours %s rejected: %v", kind, fill, err)
			}
		}
	}
	for _, props := range []map[string]json.RawMessage{
		{"background": json.RawMessage(`"#fff"`)},
		{"background": json.RawMessage(`"red"`)},
		{"background": json.RawMessage(`"#12345g"`)},
		{"background": json.RawMessage(`"#1234567"`)},
		{"background": json.RawMessage(`""`)},
		{"background": json.RawMessage(`1`)},
		{"textColor": json.RawMessage(`"tile"`)},
		{"textColor": json.RawMessage(`"#fff"`)},
		{"textColor": json.RawMessage(`"123456"`)},
		{"accentColor": json.RawMessage(`"accent"`)},
		{"accentColor": json.RawMessage(`"#00C2FF80"`)},
		{"accentColor": json.RawMessage(`null`)},
	} {
		if err := widgetLayout("chart", props).Validate(); err == nil {
			t.Fatalf("chart %v accepted", props)
		}
	}
}

func TestLiveRankingHighlights(t *testing.T) {
	type check struct {
		kind  string
		props map[string]json.RawMessage
	}
	places := json.RawMessage(`["#FFD700","#c0c0c0","#CD7F32"]`)
	valid := []check{
		{"table", map[string]json.RawMessage{"highlightTop": json.RawMessage(`"none"`)}},
		{"table", map[string]json.RawMessage{"highlightTop": json.RawMessage(`"accent"`), "showBars": json.RawMessage(`true`)}},
		{"table", map[string]json.RawMessage{"highlightTop": json.RawMessage(`"medals"`), "placeColors": places, "showBars": json.RawMessage(`false`)}},
		{"podium", map[string]json.RawMessage{"variant": json.RawMessage(`"steps"`), "showRing": json.RawMessage(`true`)}},
		{"podium", map[string]json.RawMessage{"variant": json.RawMessage(`"cards"`), "showRing": json.RawMessage(`false`), "highlightTop": json.RawMessage(`"medals"`), "placeColors": places}},
	}
	for _, item := range valid {
		layout := widgetLayout(item.kind, item.props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err != nil {
			t.Fatalf("%s %v rejected: %v", item.kind, item.props, err)
		}
	}
	invalid := []check{
		{"table", map[string]json.RawMessage{"highlightTop": json.RawMessage(`"gold"`)}},
		{"table", map[string]json.RawMessage{"highlightTop": json.RawMessage(`true`)}},
		{"table", map[string]json.RawMessage{"showBars": json.RawMessage(`"yes"`)}},
		{"table", map[string]json.RawMessage{"placeColors": json.RawMessage(`["#FFD700","#C0C0C0"]`)}},
		{"table", map[string]json.RawMessage{"placeColors": json.RawMessage(`["#FFD700","#C0C0C0","#CD7F32","#000000"]`)}},
		{"table", map[string]json.RawMessage{"placeColors": json.RawMessage(`["#FFD700","#C0C0C0","#fff"]`)}},
		{"table", map[string]json.RawMessage{"placeColors": json.RawMessage(`["gold","silver","bronze"]`)}},
		{"table", map[string]json.RawMessage{"placeColors": json.RawMessage(`"#FFD700"`)}},
		{"podium", map[string]json.RawMessage{"variant": json.RawMessage(`"tower"`)}},
		{"podium", map[string]json.RawMessage{"showRing": json.RawMessage(`1`)}},
		{"podium", map[string]json.RawMessage{"highlightTop": json.RawMessage(`"all"`)}},
		{"podium", map[string]json.RawMessage{"placeColors": json.RawMessage(`[]`)}},
		// The ranking settings belong to the table and the podium only.
		{"table", map[string]json.RawMessage{"variant": json.RawMessage(`"cards"`)}},
		{"podium", map[string]json.RawMessage{"showBars": json.RawMessage(`true`)}},
		{"chart", map[string]json.RawMessage{"highlightTop": json.RawMessage(`"accent"`)}},
		{"solves", map[string]json.RawMessage{"placeColors": places}},
	}
	for _, item := range invalid {
		layout := widgetLayout(item.kind, item.props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err == nil {
			t.Fatalf("%s %v accepted", item.kind, item.props)
		}
	}
}

func TestLivePlaceMarkersLegendAndChartLabels(t *testing.T) {
	type check struct {
		kind  string
		props map[string]json.RawMessage
	}
	images := json.RawMessage(`["/api/events/e/content-images/1","https://cdn.example.org/2.png","/api/events/e/content-images/3"]`)
	valid := []check{
		{"table", map[string]json.RawMessage{"marker": json.RawMessage(`"numbers"`), "showLegend": json.RawMessage(`true`)}},
		{"table", map[string]json.RawMessage{"marker": json.RawMessage(`"medals"`), "showLegend": json.RawMessage(`false`)}},
		{"table", map[string]json.RawMessage{"marker": json.RawMessage(`"medal-icons"`)}},
		{"table", map[string]json.RawMessage{"marker": json.RawMessage(`"images"`), "placeImages": images}},
		{"podium", map[string]json.RawMessage{"marker": json.RawMessage(`"cups"`)}},
		{"podium", map[string]json.RawMessage{"marker": json.RawMessage(`"images"`), "placeImages": images}},
		{"chart", map[string]json.RawMessage{"labels": json.RawMessage(`"chart"`)}},
		{"chart", map[string]json.RawMessage{"labels": json.RawMessage(`"none"`), "lines": json.RawMessage(`7`)}},
	}
	for _, item := range valid {
		layout := widgetLayout(item.kind, item.props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err != nil {
			t.Fatalf("%s %v rejected: %v", item.kind, item.props, err)
		}
	}
	invalid := []check{
		{"table", map[string]json.RawMessage{"marker": json.RawMessage(`"stars"`)}},
		{"table", map[string]json.RawMessage{"marker": json.RawMessage(`1`)}},
		{"table", map[string]json.RawMessage{"showLegend": json.RawMessage(`"yes"`)}},
		{"table", map[string]json.RawMessage{"placeImages": json.RawMessage(`["/a.png","/b.png"]`)}},
		{"table", map[string]json.RawMessage{"placeImages": json.RawMessage(`["/a.png","/b.png","/c.png","/d.png"]`)}},
		// Plain HTTP, protocol-relative and script addresses are refused like for logos.
		{"table", map[string]json.RawMessage{"placeImages": json.RawMessage(`["http://cdn.example.org/1.png","/b.png","/c.png"]`)}},
		{"podium", map[string]json.RawMessage{"placeImages": json.RawMessage(`["//evil.example/1.png","/b.png","/c.png"]`)}},
		{"podium", map[string]json.RawMessage{"placeImages": json.RawMessage(`["javascript:alert(1)","/b.png","/c.png"]`)}},
		{"podium", map[string]json.RawMessage{"placeImages": json.RawMessage(`["","/b.png","/c.png"]`)}},
		{"podium", map[string]json.RawMessage{"placeImages": json.RawMessage(`"/a.png"`)}},
		{"chart", map[string]json.RawMessage{"labels": json.RawMessage(`"legend"`)}},
		// Each setting belongs to its widgets only.
		{"podium", map[string]json.RawMessage{"showLegend": json.RawMessage(`true`)}},
		{"chart", map[string]json.RawMessage{"marker": json.RawMessage(`"numbers"`)}},
		{"table", map[string]json.RawMessage{"labels": json.RawMessage(`"chart"`)}},
		{"solves", map[string]json.RawMessage{"placeImages": images}},
	}
	for _, item := range invalid {
		layout := widgetLayout(item.kind, item.props)
		layout.Widgets[0].H = 3
		if err := layout.Validate(); err == nil {
			t.Fatalf("%s %v accepted", item.kind, item.props)
		}
	}
}
