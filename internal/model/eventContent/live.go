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
}

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
	return LiveLayout{
		Version: 1, Theme: "dark", Aspect: "16:9", RefreshSeconds: LiveRefreshDefault,
		Screen: LiveScreen{Width: 1920, Height: 1080, Anchor: "full", TextScale: 1},
		Grid:   LiveGrid{Cols: 12, Rows: 8},
		Widgets: []LiveWidget{
			{ID: "title", Type: "title", X: 1, Y: 1, W: 10, H: 1, Props: map[string]json.RawMessage{}},
			{ID: "chart", Type: "chart", X: 1, Y: 2, W: 8, H: 6, Props: map[string]json.RawMessage{}},
			{ID: "table", Type: "table", X: 9, Y: 2, W: 4, H: 6, Props: map[string]json.RawMessage{}},
			{ID: "organizers", Type: "logos", X: 1, Y: 8, W: 3, H: 1, Props: map[string]json.RawMessage{"mode": json.RawMessage(`"fixed"`), "title": json.RawMessage(`"Організатори"`)}},
			{ID: "partners", Type: "logos", X: 4, Y: 8, W: 9, H: 1, Props: map[string]json.RawMessage{"mode": json.RawMessage(`"carousel"`), "title": json.RawMessage(`"Партнери"`)}},
			{ID: "timer", Type: "timer", X: 11, Y: 1, W: 2, H: 1, Props: map[string]json.RawMessage{}},
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
}

var livePropTypes = map[string]map[string]string{
	"title":        {"subtitle": "text"},
	"timer":        {"format": "timer-format", "showLabel": "boolean", "showSeconds": "boolean", "source": "timer-source", "target": "time", "label": "short-text", "labelSize": "label-size"},
	"chart":        {"lines": "lines"},
	"table":        {"rowsPerPage": "rows", "pageSeconds": "seconds"},
	"ad_table":     {"columns": "text-list"},
	"logos":        {"mode": "logo-mode", "title": "text", "logos": "logo-list", "items": "logo-items", "color": "logo-color", "speed": "speed", "paused": "boolean"},
	"solves":       {"rows": "rows"},
	"announcement": {"text": "text"},
	"qr":           {"url": "url", "value": "qr-value", "caption": "short-text"},
}

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
	if l.Grid.Cols < minGrid || l.Grid.Cols > 48 || l.Grid.Rows < minGrid || l.Grid.Rows > 32 {
		return errors.New("live grid size is invalid")
	}
	if err := l.validateFormats(minGrid); err != nil {
		return err
	}
	if len(l.Widgets) > 48 {
		return errors.New("too many live widgets")
	}
	ids := make(map[string]bool, len(l.Widgets))
	occupied := make(map[int]bool)
	for _, widget := range l.Widgets {
		if widget.ID == "" || len(widget.ID) > 64 || ids[widget.ID] {
			return errors.New("live widget IDs must be unique and non-empty")
		}
		ids[widget.ID] = true
		minimum, ok := liveMinimums[widget.Type]
		if !ok {
			return fmt.Errorf("live widget %q has an unsupported type", widget.ID)
		}
		if widget.W < minimum.w || widget.H < minimum.h || widget.X < 1 || widget.Y < 1 || widget.X+widget.W-1 > l.Grid.Cols || widget.Y+widget.H-1 > l.Grid.Rows {
			return fmt.Errorf("live widget %q is outside the grid or smaller than allowed", widget.ID)
		}
		for y := widget.Y; y < widget.Y+widget.H; y++ {
			for x := widget.X; x < widget.X+widget.W; x++ {
				cell := y*l.Grid.Cols + x
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
		case "timer-format", "logo-mode", "speed", "timer-source", "label-size", "logo-color":
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

// validateFormats: a format is another shape than the base; «auto» holds
// nothing, «custom» places existing widgets on its own grid within the grid,
// at least their minimum size, once each and without overlaps.
func (l LiveLayout) validateFormats(minGrid int) error {
	types := make(map[string]string, len(l.Widgets))
	for _, widget := range l.Widgets {
		types[widget.ID] = widget.Type
	}
	for key, format := range l.Formats {
		if !liveFormatKeys[key] || key == l.Aspect {
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
		return value == "auto" || value == "start" || value == "finish" || value == "freeze" || value == "custom"
	case "label-size":
		return value == "s" || value == "m" || value == "l"
	case "logo-color":
		return value == "original" || value == "mono"
	default:
		return false
	}
}
