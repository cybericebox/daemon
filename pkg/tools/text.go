package tools

import (
	"strings"
	"unicode"
)

// UnsafeDisplayRune reports a character a display name must not hold: control characters, the invisible
// format characters (zero-width space, joiners, the byte order mark, soft hyphen), the bidirectional controls
// that reverse how neighbouring text is shown (U+202A..U+202E, U+2066..U+2069, the marks) and the line and
// paragraph separators. Such characters let one name look like another on a board or in a list.
func UnsafeDisplayRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

// HasUnsafeDisplayText reports whether s holds a character UnsafeDisplayRune refuses.
func HasUnsafeDisplayText(s string) bool {
	return strings.IndexFunc(s, UnsafeDisplayRune) >= 0
}

// StripUnsafeDisplayText removes what UnsafeDisplayRune refuses, for text that comes from a provider and cannot be
// sent back for correction.
func StripUnsafeDisplayText(s string) string {
	return strings.Map(func(r rune) rune {
		if UnsafeDisplayRune(r) {
			return -1
		}
		return r
	}, s)
}
