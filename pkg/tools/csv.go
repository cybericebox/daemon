package tools

import (
	"strings"
	"unicode"
)

// formulaStarts are the characters a spreadsheet reads as the start of a formula, in their ASCII and full-width
// forms.
const formulaStarts = "=+-@＝＋－＠"

// CSVText neutralizes a text cell that a spreadsheet could run as a formula: when the first character that is not
// whitespace or invisible is one of = + - @ (full-width forms included), or the cell starts with a tab, carriage
// return or line feed, it is prefixed with an apostrophe. Every CSV export of free text (names, answers, error
// messages) goes through it.
func CSVText(value string) string {
	if value == "" {
		return value
	}
	if strings.ContainsRune("\t\r\n", rune(value[0])) {
		return "'" + value
	}
	for _, r := range value {
		if unicode.IsSpace(r) || UnsafeDisplayRune(r) {
			continue
		}
		if strings.ContainsRune(formulaStarts, r) {
			return "'" + value
		}
		break
	}
	return value
}
