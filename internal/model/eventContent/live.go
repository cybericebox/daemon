package eventContentModel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// LiveLayout is the published or draft canvas for a projector/LED screen.
// Coordinates are one-based cells of its grid, independent of pixel size.
type LiveLayout struct {
	Version int64        `json:"version"`
	Theme   string       `json:"theme"`
	Aspect  string       `json:"aspect"`
	Screen  LiveScreen   `json:"screen"`
	Grid    LiveGrid     `json:"grid"`
	Widgets []LiveWidget `json:"widgets"`
	// RefreshSeconds is how often an open screen takes new results: the
	// stream poll interval and the polling fallback. 0 = LiveRefreshDefault
	// (layouts stored before the setting existed).
	RefreshSeconds int `json:"refreshSeconds,omitempty"`
	// Formats are the other screen shapes: «auto» derives them from this
	// layout, «custom» places the same widgets on its own grid.
	Formats map[string]LiveFormat `json:"formats,omitempty"`
	// Scenes are the layouts of the other event states: «before» the start,
	// «frozen» while the results freeze, «finished» after the finish. This
	// layout is the «running» scene; a state without its own scene shows it.
	// The screen settings (theme, aspect, screen, refresh) are shared.
	Scenes map[string]LiveScene `json:"scenes,omitempty"`
}

// LiveScene is the canvas of one event state: its own grid, widgets and
// screen shapes, checked by the same rules as the running layout.
type LiveScene struct {
	Grid    LiveGrid              `json:"grid"`
	Widgets []LiveWidget          `json:"widgets"`
	Formats map[string]LiveFormat `json:"formats,omitempty"`
}

// liveSceneKeys are the states with a scene of their own besides «running».
var liveSceneKeys = map[string]bool{"before": true, "frozen": true, "finished": true}

// LiveFormat is the layout of one screen shape besides the base.
type LiveFormat struct {
	Mode       string          `json:"mode"`
	Grid       *LiveGrid       `json:"grid,omitempty"`
	Placements []LivePlacement `json:"placements,omitempty"`
}

// LivePlacement puts a base widget (by ID) on a custom format's grid.
type LivePlacement struct {
	ID string `json:"id"`
	X  int    `json:"x"`
	Y  int    `json:"y"`
	W  int    `json:"w"`
	H  int    `json:"h"`
}

// liveFormatKeys are the shapes a layout can hold besides its base aspect.
var liveFormatKeys = map[string]bool{"16:9": true, "16:10": true, "4:3": true}

const (
	LiveRefreshDefault = 5
	liveRefreshMin     = 2
	liveRefreshMax     = 30
)

// Refresh is the effective results refresh interval in seconds.
func (l LiveLayout) Refresh() int {
	if l.RefreshSeconds == 0 {
		return LiveRefreshDefault
	}
	return l.RefreshSeconds
}

type LiveScreen struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Anchor    string  `json:"anchor"`
	TextScale float64 `json:"textScale"`
	// SafeArea is the margin around the canvas, in % of each side, for TVs
	// that crop the picture edges (overscan): 0, 2.5 or 5.
	SafeArea float64 `json:"safeArea,omitempty"`
}

type LiveGrid struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type LiveWidget struct {
	ID    string                     `json:"id"`
	Type  string                     `json:"type"`
	X     int                        `json:"x"`
	Y     int                        `json:"y"`
	W     int                        `json:"w"`
	H     int                        `json:"h"`
	Props map[string]json.RawMessage `json:"props"`
}

func DefaultLiveLayout() LiveLayout {
	props := func(pairs ...string) map[string]json.RawMessage {
		out := make(map[string]json.RawMessage, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			out[pairs[i]] = json.RawMessage(pairs[i+1])
		}
		return out
	}
	organizers := func(id string) LiveWidget {
		return LiveWidget{ID: id, Type: "logos", X: 1, Y: 8, W: 3, H: 1, Props: props("mode", `"fixed"`, "title", `"Організатори"`)}
	}
	partners := func(id string) LiveWidget {
		return LiveWidget{ID: id, Type: "logos", X: 4, Y: 8, W: 9, H: 1, Props: props("mode", `"carousel"`, "title", `"Партнери"`)}
	}
	return LiveLayout{
		Version: 1, Theme: "dark", Aspect: "16:9", RefreshSeconds: LiveRefreshDefault,
		Screen: LiveScreen{Width: 1920, Height: 1080, Anchor: "full", TextScale: 1},
		Grid:   LiveGrid{Cols: 12, Rows: 8},
		Widgets: []LiveWidget{
			{ID: "title", Type: "title", X: 1, Y: 1, W: 9, H: 1, Props: props()},
			{ID: "chart", Type: "chart", X: 1, Y: 2, W: 8, H: 6, Props: props()},
			{ID: "table", Type: "table", X: 9, Y: 2, W: 4, H: 6, Props: props()},
			organizers("organizers"),
			partners("partners"),
			{ID: "timer", Type: "timer", X: 10, Y: 1, W: 3, H: 1, Props: props()},
		},
		// The default show: «Перед стартом» → this board → «Підсумки».
		Scenes: map[string]LiveScene{
			"before": {Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{
				{ID: "before-title", Type: "title", X: 1, Y: 1, W: 8, H: 2, Props: props()},
				{ID: "before-timer", Type: "timer", X: 1, Y: 3, W: 8, H: 3, Props: props("align", `"left"`)},
				{ID: "before-facts", Type: "facts", X: 1, Y: 6, W: 8, H: 2, Props: props("items", `["teams","tasks","start"]`)},
				{ID: "before-qr", Type: "qr", X: 9, Y: 1, W: 4, H: 7, Props: props("value", `"/"`, "caption", `"Сайт заходу"`)},
				organizers("before-organizers"),
				partners("before-partners"),
			}},
			"finished": {Grid: LiveGrid{Cols: 12, Rows: 8}, Widgets: []LiveWidget{
				{ID: "finished-title", Type: "title", X: 1, Y: 1, W: 9, H: 1, Props: props("subtitle", `"Підсумки"`)},
				{ID: "finished-timer", Type: "timer", X: 10, Y: 1, W: 3, H: 1, Props: props()},
				{ID: "finished-podium", Type: "podium", X: 1, Y: 2, W: 12, H: 3, Props: props()},
				{ID: "finished-table", Type: "table", X: 1, Y: 5, W: 12, H: 3, Props: props("columns", `2`, "offset", `3`, "limit", `8`, "rowsPerPage", `4`, "showCaption", `false`)},
				organizers("finished-organizers"),
				partners("finished-partners"),
			}},
		},
	}
}

type liveSize struct{ w, h int }

// liveGridMinSize matches the editor: smaller grids cannot host a usable
// layout (the title alone needs 3 columns). It applies to writes only;
// layouts stored before it existed may be smaller and must still load.
const (
	liveGridMinSize       = 3
	liveStoredGridMinSize = 1
)

var liveMinimums = map[string]liveSize{
	"title": {3, 1}, "timer": {2, 1}, "chart": {4, 3},
	"table": {3, 3}, "ad_table": {6, 4}, "logos": {2, 1},
	"solves": {3, 2}, "announcement": {3, 1}, "qr": {1, 1},
	"podium": {4, 2}, "facts": {3, 1}, "stages": {3, 2}, "clock": {2, 1},
}

// liveCommonProps are the settings every widget has: its text size (K3) and
// its caption, shown or not and with an own text (K4).
var liveCommonProps = map[string]string{"textSize": "text-size", "showCaption": "boolean", "caption": "short-text"}

var livePropTypes = map[string]map[string]string{
	"title": {"subtitle": "text", "subtitleStage": "boolean", "showLogo": "boolean", "align": "align"},
	"timer": {"format": "timer-format", "showLabel": "boolean", "showSeconds": "boolean", "source": "timer-source", "target": "time", "label": "short-text", "labelSize": "label-size",
		"digits": "timer-digits", "atZero": "timer-zero", "zeroText": "short-text", "lastMinutes": "last-minutes", "align": "align"},
	"chart":        {"lines": "lines"},
	"table":        {"rowsPerPage": "rows", "pageSeconds": "seconds", "pinTop": "pin-top", "columns": "table-columns", "limit": "limit", "offset": "table-offset"},
	"ad_table":     {"columns": "text-list"},
	"logos":        {"mode": "logo-mode", "title": "text", "logos": "logo-list", "items": "logo-items", "color": "logo-color", "speed": "speed", "paused": "boolean", "rows": "logo-rows"},
	"solves":       {"rows": "rows", "mode": "solves-mode"},
	"announcement": {"text": "text", "align": "align"},
	"qr":           {"url": "url", "value": "qr-value", "caption": "short-text"},
	"podium":       {"showPoints": "boolean"},
	"facts":        {"items": "fact-list"},
	"stages":       {"showDone": "boolean"},
	"clock":        {"showSeconds": "boolean", "label": "short-text", "align": "align"},
}

// liveFactKeys are the numbers a facts widget can show; all come from the
// live results, the event and its stages.
var liveFactKeys = map[string]bool{"teams": true, "tasks": true, "solves": true, "solvedTasks": true, "firstBlood": true, "start": true, "finish": true}

// Validate is the write-side check for a layout a manager saves or publishes.
func (l LiveLayout) Validate() error {
	return l.validate(liveGridMinSize)
}

// ValidateStored is the read-side check: it keeps every safety rule but
// tolerates legacy grids below the current editor minimum.
func (l LiveLayout) ValidateStored() error {
	return l.validate(liveStoredGridMinSize)
}

func (l LiveLayout) validate(minGrid int) error {
	if l.Version < 1 || l.Version > 1<<31 {
		return errors.New("live layout version is invalid")
	}
	if l.Theme != "dark" && l.Theme != "light" {
		return errors.New("live layout theme is invalid")
	}
	switch l.Aspect {
	case "16:9", "16:10", "4:3", "5:3", "custom":
	default:
		return errors.New("live layout aspect is invalid")
	}
	if l.Screen.Width < 320 || l.Screen.Width > 7680 || l.Screen.Height < 240 || l.Screen.Height > 4320 || l.Screen.Width <= l.Screen.Height {
		return errors.New("live screen size must be landscape and within supported limits")
	}
	if l.RefreshSeconds != 0 && (l.RefreshSeconds < liveRefreshMin || l.RefreshSeconds > liveRefreshMax) {
		return errors.New("live refresh interval must be between 2 and 30 seconds")
	}
	if l.Screen.Anchor != "full" && l.Screen.Anchor != "top-left" {
		return errors.New("live screen anchor is invalid")
	}
	if l.Screen.TextScale < 0.8 || l.Screen.TextScale > 1.5 {
		return errors.New("live text scale is invalid")
	}
	if l.Screen.SafeArea != 0 && l.Screen.SafeArea != 2.5 && l.Screen.SafeArea != 5 {
		return errors.New("live safe area is invalid")
	}
	if err := validateLiveCanvas(l.Aspect, l.Grid, l.Widgets, l.Formats, minGrid); err != nil {
		return err
	}
	for key, scene := range l.Scenes {
		if !liveSceneKeys[key] {
			return fmt.Errorf("live scene %q is not an event state", key)
		}
		if err := validateLiveCanvas(l.Aspect, scene.Grid, scene.Widgets, scene.Formats, minGrid); err != nil {
			return fmt.Errorf("live scene %q: %w", key, err)
		}
	}
	return nil
}

// validateLiveCanvas checks one scene: its grid, its widgets inside it at
// least their minimum size and without overlaps, their settings, and its
// other screen shapes.
func validateLiveCanvas(aspect string, grid LiveGrid, widgets []LiveWidget, formats map[string]LiveFormat, minGrid int) error {
	if grid.Cols < minGrid || grid.Cols > 48 || grid.Rows < minGrid || grid.Rows > 32 {
		return errors.New("live grid size is invalid")
	}
	if err := validateLiveFormats(aspect, widgets, formats, minGrid); err != nil {
		return err
	}
	if len(widgets) > 48 {
		return errors.New("too many live widgets")
	}
	ids := make(map[string]bool, len(widgets))
	occupied := make(map[int]bool)
	for _, widget := range widgets {
		if widget.ID == "" || len(widget.ID) > 64 || ids[widget.ID] {
			return errors.New("live widget IDs must be unique and non-empty")
		}
		ids[widget.ID] = true
		minimum, ok := liveMinimums[widget.Type]
		if !ok {
			return fmt.Errorf("live widget %q has an unsupported type", widget.ID)
		}
		if widget.W < minimum.w || widget.H < minimum.h || widget.X < 1 || widget.Y < 1 || widget.X+widget.W-1 > grid.Cols || widget.Y+widget.H-1 > grid.Rows {
			return fmt.Errorf("live widget %q is outside the grid or smaller than allowed", widget.ID)
		}
		for y := widget.Y; y < widget.Y+widget.H; y++ {
			for x := widget.X; x < widget.X+widget.W; x++ {
				cell := y*grid.Cols + x
				if occupied[cell] {
					return fmt.Errorf("live widget %q overlaps another widget", widget.ID)
				}
				occupied[cell] = true
			}
		}
		if err := widget.validateProps(); err != nil {
			return fmt.Errorf("live widget %q: %w", widget.ID, err)
		}
		if err := widget.validateTimer(); err != nil {
			return fmt.Errorf("live widget %q: %w", widget.ID, err)
		}
	}
	return nil
}

func (w LiveWidget) validateProps() error {
	allowed := livePropTypes[w.Type]
	for key, raw := range w.Props {
		kind, ok := allowed[key]
		if !ok {
			kind, ok = liveCommonProps[key]
		}
		if !ok {
			return fmt.Errorf("unsupported property %q", key)
		}
		switch kind {
		case "boolean":
			var value bool
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("invalid boolean property %q", key)
			}
		case "text", "short-text":
			var value string
			if json.Unmarshal(raw, &value) != nil || len(value) > 500 || kind == "short-text" && utf8.RuneCountInString(value) > 60 {
				return fmt.Errorf("invalid text property %q", key)
			}
		case "time":
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("invalid time property %q", key)
			}
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return fmt.Errorf("invalid time property %q", key)
			}
		case "lines", "rows", "seconds":
			var value int
			if json.Unmarshal(raw, &value) != nil || value < 1 || value > 60 || kind == "lines" && (value < 5 || value > 10) {
				return fmt.Errorf("invalid number property %q", key)
			}
		case "pin-top", "table-columns", "table-offset", "logo-rows", "last-minutes", "limit":
			var value int
			if json.Unmarshal(raw, &value) != nil || !liveNumberChoice(kind, value) {
				return fmt.Errorf("invalid number property %q", key)
			}
		case "fact-list":
			var values []string
			if json.Unmarshal(raw, &values) != nil || len(values) < 1 || len(values) > 6 {
				return fmt.Errorf("invalid list property %q", key)
			}
			seen := make(map[string]bool, len(values))
			for _, value := range values {
				if !liveFactKeys[value] || seen[value] {
					return fmt.Errorf("invalid list property %q", key)
				}
				seen[value] = true
			}
		case "timer-format", "logo-mode", "speed", "timer-source", "label-size", "logo-color", "text-size", "align", "timer-digits", "timer-zero", "solves-mode":
			var value string
			if json.Unmarshal(raw, &value) != nil || !liveEnumValue(kind, value) {
				return fmt.Errorf("invalid choice property %q", key)
			}
		case "text-list", "logo-list":
			var values []string
			if json.Unmarshal(raw, &values) != nil || len(values) > 32 {
				return fmt.Errorf("invalid list property %q", key)
			}
			for _, value := range values {
				if value == "" || len(value) > 2048 || kind == "logo-list" && (!validLiveURL(value) || strings.HasPrefix(value, "http://")) {
					return fmt.Errorf("invalid list property %q", key)
				}
			}
		case "logo-items":
			var items []map[string]json.RawMessage
			if json.Unmarshal(raw, &items) != nil || len(items) > 32 || !validLogoItems(items) {
				return fmt.Errorf("invalid logo list %q", key)
			}
		case "qr-value":
			var value string
			if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" || len(value) > 500 || looksLikeURL(value) && !validLiveURL(value) {
				return fmt.Errorf("invalid QR content %q", key)
			}
		case "url":
			var value string
			if json.Unmarshal(raw, &value) != nil || !validLiveURL(value) {
				return fmt.Errorf("invalid URL property %q", key)
			}
		}
	}
	return nil
}

// validateLiveFormats: a format is another shape than the base; «auto»
// holds nothing, «custom» places existing widgets of its scene on its own
// grid within the grid, at least their minimum size, once each and without
// overlaps.
func validateLiveFormats(aspect string, widgets []LiveWidget, formats map[string]LiveFormat, minGrid int) error {
	types := make(map[string]string, len(widgets))
	for _, widget := range widgets {
		types[widget.ID] = widget.Type
	}
	for key, format := range formats {
		if !liveFormatKeys[key] || key == aspect {
			return fmt.Errorf("live format %q is not another screen shape", key)
		}
		switch format.Mode {
		case "auto":
			if format.Grid != nil || len(format.Placements) > 0 {
				return fmt.Errorf("live format %q is automatic and holds no layout", key)
			}
		case "custom":
			if format.Grid == nil || format.Grid.Cols < minGrid || format.Grid.Cols > 48 || format.Grid.Rows < minGrid || format.Grid.Rows > 32 {
				return fmt.Errorf("live format %q grid size is invalid", key)
			}
			placed := make(map[string]bool, len(format.Placements))
			occupied := make(map[int]bool)
			for _, place := range format.Placements {
				kind, ok := types[place.ID]
				if !ok || placed[place.ID] {
					return fmt.Errorf("live format %q places an unknown or repeated widget %q", key, place.ID)
				}
				placed[place.ID] = true
				minimum := liveMinimums[kind]
				if place.W < minimum.w || place.H < minimum.h || place.X < 1 || place.Y < 1 || place.X+place.W-1 > format.Grid.Cols || place.Y+place.H-1 > format.Grid.Rows {
					return fmt.Errorf("live format %q: widget %q is outside the grid or smaller than allowed", key, place.ID)
				}
				for y := place.Y; y < place.Y+place.H; y++ {
					for x := place.X; x < place.X+place.W; x++ {
						cell := y*format.Grid.Cols + x
						if occupied[cell] {
							return fmt.Errorf("live format %q: widget %q overlaps another widget", key, place.ID)
						}
						occupied[cell] = true
					}
				}
			}
		default:
			return fmt.Errorf("live format %q mode is invalid", key)
		}
	}
	return nil
}

// validateTimer: a custom time source needs its target time.
func (w LiveWidget) validateTimer() error {
	if w.Type != "timer" {
		return nil
	}
	var source string
	if raw, ok := w.Props["source"]; ok && json.Unmarshal(raw, &source) == nil && source == "custom" {
		if _, ok := w.Props["target"]; !ok {
			return errors.New("a custom timer needs its target time")
		}
	}
	return nil
}

// validLogoItems: each logo has a src and an optional dark-theme file, both
// uploaded files or HTTPS images; nothing else.
func validLogoItems(items []map[string]json.RawMessage) bool {
	image := func(raw json.RawMessage) bool {
		var value string
		return json.Unmarshal(raw, &value) == nil && validLiveURL(value) && !strings.HasPrefix(value, "http://")
	}
	for _, item := range items {
		src, ok := item["src"]
		if !ok || !image(src) {
			return false
		}
		for key, raw := range item {
			if key != "src" && (key != "dark" || !image(raw)) {
				return false
			}
		}
	}
	return true
}

// looksLikeURL: QR content that starts like a web address must be a valid one.
func looksLikeURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.")
}

func validLiveURL(value string) bool {
	if len(value) == 0 || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (((parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "") || (strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")))
}

func liveEnumValue(kind, value string) bool {
	switch kind {
	case "timer-format":
		return value == "jeopardy" || value == "ad"
	case "logo-mode":
		return value == "fixed" || value == "carousel"
	case "speed":
		return value == "slow" || value == "normal" || value == "fast"
	case "timer-source":
		return value == "auto" || value == "start" || value == "finish" || value == "freeze" || value == "custom" || value == "stage-end" || value == "stage-next"
	case "label-size":
		return value == "s" || value == "m" || value == "l"
	case "logo-color":
		return value == "original" || value == "mono"
	case "text-size":
		return value == "s" || value == "m" || value == "l" || value == "xl"
	case "align":
		return value == "left" || value == "center" || value == "right"
	case "timer-digits":
		return value == "full" || value == "compact" || value == "words"
	case "timer-zero":
		return value == "word" || value == "text" || value == "hide"
	case "solves-mode":
		return value == "recent" || value == "first"
	default:
		return false
	}
}

// liveNumberChoice: the whole-number settings with a fixed set of values.
func liveNumberChoice(kind string, value int) bool {
	switch kind {
	case "pin-top":
		return value == 0 || value == 3 || value == 5
	case "table-columns":
		return value == 1 || value == 2
	case "table-offset":
		return value == 0 || value == 3
	case "logo-rows":
		return value >= 1 && value <= 3
	case "last-minutes":
		return value == 0 || value == 1 || value == 3 || value == 5 || value == 10 || value == 15
	case "limit":
		return value >= 0 && value <= 60
	default:
		return false
	}
}
