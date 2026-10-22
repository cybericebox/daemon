package eventContentModel

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofrs/uuid"
)

// LiveDisplay is one live screen of an event («Екран 1», «Екран 2», …): its
// own name, published layout, draft and screen link, so a hall screen and
// side screens show different things. An event has 1..LiveDisplaysMax of
// them in the editor order; the first is the screen of the single-screen
// routes. (LiveScreen is the canvas size of a layout, hence the name.)
type LiveDisplay struct {
	ID       uuid.UUID
	EventID  uuid.UUID
	Position int32
	Name     string
	// Layout is the published layout; Draft the editor's unpublished one.
	Layout    LiveLayout
	Draft     *LiveLayout
	CreatedAt time.Time
	UpdatedAt time.Time
}

const (
	// LiveDisplaysMax bounds the screens of one event; the widget limit
	// (liveWidgetsTotal) applies to each screen's layout on its own.
	LiveDisplaysMax = 10
	// LiveDisplayNameMax is the longest screen name, in characters.
	LiveDisplayNameMax = 40
)

// NormalizeLiveDisplayName trims a screen name and checks it: 1..40
// characters, no control characters. Names need not be unique.
func NormalizeLiveDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > LiveDisplayNameMax || !utf8.ValidString(name) {
		return "", ErrLiveScreenNameInvalid.WithError(errors.New("a screen name has 1 to 40 characters")).Err()
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrLiveScreenNameInvalid.WithError(errors.New("a screen name has no control characters")).Err()
		}
	}
	return name, nil
}

// DefaultLiveDisplayName is the name of a screen created without one:
// «Екран N» with the next number, skipping names already taken.
func DefaultLiveDisplayName(existing []LiveDisplay) string {
	taken := make(map[string]bool, len(existing))
	for _, display := range existing {
		taken[display.Name] = true
	}
	for n := len(existing) + 1; ; n++ {
		if name := fmt.Sprintf("Екран %d", n); !taken[name] {
			return name
		}
	}
}

// ValidateStored applies the read-side layout checks to the screen.
func (d LiveDisplay) ValidateStored() error {
	if err := d.Layout.ValidateStored(); err != nil {
		return err
	}
	if d.Draft != nil {
		return d.Draft.ValidateStored()
	}
	return nil
}
