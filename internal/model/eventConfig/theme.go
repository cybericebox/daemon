package eventConfigModel

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

const defaultBrand = "#211A52"

// Theme stores the two admin inputs and the three contrast-adjusted CSS
// inputs. The remaining palette is calculated by ds-v2 tokens in the browser.
type Theme struct {
	Brand       string
	Accent      string
	AccentLight string
	AccentDark  string
	AccentLive  string
	Version     int64
}

func DefaultTheme() Theme {
	return Theme{
		Brand: defaultBrand, AccentLight: defaultBrand,
		AccentDark: "#E6E6EE", AccentLive: "#FFFFFF", Version: 1,
	}
}

// EmailOnAccent returns the text color with the stronger contrast against the
// chosen email accent. An unset accent uses the event brand.
func (t Theme) EmailOnAccent() string {
	value := t.Accent
	if value == "" {
		value = t.Brand
	}
	color, ok := parseHexColor(value)
	if !ok {
		return "#FFFFFF"
	}
	if color.luminance() > 0.179 {
		return "#000000"
	}
	return "#FFFFFF"
}

// SetTheme validates the editable colors and derives readable accent values
// once, when the settings are saved. An unchanged value leaves the version and
// optimistic-lock timestamp untouched.
func (c *EventConfig) SetTheme(brand, accent string, now time.Time, by uuid.UUID) error {
	updated, valid := deriveTheme(brand, accent)
	if !valid {
		return ErrThemeInvalid.Err()
	}
	if c.Theme.Brand == updated.Brand && c.Theme.Accent == updated.Accent {
		return nil
	}
	updated.Version = c.Theme.Version + 1
	if updated.Version < 2 {
		updated.Version = 2
	}
	c.Theme = updated
	c.touch(now, by)
	return nil
}

type rgb struct{ r, g, b uint8 }

func parseHexColor(value string) (rgb, bool) {
	if len(value) != 7 || value[0] != '#' {
		return rgb{}, false
	}
	var channels [3]uint8
	for i := range channels {
		v, err := strconv.ParseUint(value[1+i*2:3+i*2], 16, 8)
		if err != nil {
			return rgb{}, false
		}
		channels[i] = uint8(v)
	}
	return rgb{channels[0], channels[1], channels[2]}, true
}

func (c rgb) hex() string { return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b) }

func (c rgb) luminance() float64 {
	linear := func(value uint8) float64 {
		s := float64(value) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

func contrast(a, b rgb) float64 {
	x, y := a.luminance(), b.luminance()
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

func mix(a, b rgb, share float64) rgb {
	channel := func(x, y uint8) uint8 {
		return uint8(math.Round(float64(x)*(1-share) + float64(y)*share))
	}
	return rgb{channel(a.r, b.r), channel(a.g, b.g), channel(a.b, b.b)}
}

func readable(accent, background rgb) string {
	target := rgb{0, 0, 0}
	if background.luminance() <= 0.4 {
		target = rgb{255, 255, 255}
	}
	for step := 0; step <= 50; step++ {
		candidate := mix(accent, target, float64(step)*0.02)
		if contrast(candidate, background) >= 4.5 {
			return candidate.hex()
		}
	}
	return target.hex()
}

func deriveTheme(brandInput, accentInput string) (Theme, bool) {
	brand, ok := parseHexColor(strings.TrimSpace(brandInput))
	if !ok {
		return Theme{}, false
	}
	result := DefaultTheme()
	result.Brand = brand.hex()
	accentInput = strings.TrimSpace(accentInput)
	if accentInput == "" {
		result.AccentLight = result.Brand
		return result, true
	}
	accent, ok := parseHexColor(accentInput)
	if !ok {
		return Theme{}, false
	}
	result.Accent = accent.hex()
	result.AccentLight = readable(accent, rgb{247, 247, 249})
	result.AccentDark = readable(accent, mix(rgb{64, 63, 65}, brand, 0.25))
	result.AccentLive = readable(accent, brand)
	return result, true
}
